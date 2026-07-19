package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

func nowUTC() time.Time {
	return time.Now().UTC()
}

// preloadLimiter はプリロード実行時に使う共通レートリミッター。
// Discordのグローバル制限(約50 req/秒)の半分以下、20 req/秒に制限する。
var preloadLimiter = newRateLimiter(20)

func init() {
	os.MkdirAll("./user", 0750)
	os.MkdirAll("./guilds", 0750)
}

func main() {
	preloadFlag := flag.String("preload", "", "yyyymmdd 形式の日付。指定するとその日まで遡って全ギルド・メンバー・メッセージ履歴を取得します")
	flag.Parse()

	var preloadUntil *time.Time
	if *preloadFlag != "" {
		t, err := time.ParseInLocation("20060102", *preloadFlag, time.UTC)
		if err != nil {
			log.Fatalf("--preload の形式が不正です(yyyymmddで指定してください): %v", err)
		}
		preloadUntil = &t
	}

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("環境変数 DISCORD_TOKEN が設定されていません")
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Discordセッションの作成に失敗しました: %v", err)
	}

	dg.Identify.Intents =
		discordgo.IntentsGuilds |
			discordgo.IntentsGuildMessages |
			discordgo.IntentsDirectMessages |
			discordgo.IntentMessageContent |
			discordgo.IntentsGuildEmojis

	dg.AddHandler(onMessageCreate)
	dg.AddHandler(onMessageEdit)
	dg.AddHandler(onMessageDelete)
	dg.AddHandler(onGuildMemberAdd)
	dg.AddHandler(onGuildMemberRemove)
	dg.AddHandler(onGuildMemberUpdate)
	dg.AddHandler(onChannelUpdate)
	dg.AddHandler(onGuildUpdate)
	dg.AddHandler(onUserUpdate)

	if preloadUntil != nil {
		// Readyイベント受信後(ギルド一覧がStateに載った後)にプリロードを1度だけ実行する。
		var once bool
		dg.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
			if once {
				return
			}
			once = true
			go runPreload(s, *preloadUntil)
		})
	}

	err = dg.Open()
	if err != nil {
		log.Fatalf("Discordへの接続に失敗しました: %v", err)
	}
	defer dg.Close()

	fmt.Println("Botが起動しました。Ctrl+C で終了します。")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}

// ==================== プリロード(過去データの一括取得) ====================
//
// --preload=yyyymmdd を指定した場合、Bot起動時にその日まで遡って
// 全ギルドの設定・メンバー一覧・全チャンネルのメッセージ履歴を取得して保存する。
// Discordのレート制限(概ね50 req/秒)の半分以下(20 req/秒)で処理する。

func runPreload(s *discordgo.Session, until time.Time) {
	fmt.Printf("[プリロード開始] %s まで遡ってデータを取得します\n", until.Format("2006-01-02"))

	guilds := s.State.Guilds
	for _, g := range guilds {
		preloadLimiter.wait()
		full, err := s.Guild(g.ID)
		if err != nil {
			log.Printf("警告: ギルド(%s)の取得に失敗しました: %v", g.ID, err)
			continue
		}
		fmt.Printf("[プリロード] ギルド %s (%s) の処理を開始します\n", full.Name, full.ID)

		saveGuildDataFromGuild(full)
		preloadMembers(s, full.ID)
		preloadChannels(s, full.ID, until)

		fmt.Printf("[プリロード] ギルド %s (%s) の処理が完了しました\n", full.Name, full.ID)
	}

	fmt.Println("[プリロード完了] すべてのギルドの処理が完了しました")
}

// preloadMembers はギルドの全メンバーをページングで取得し、Previous操作として記録する。
func preloadMembers(s *discordgo.Session, guildID string) {
	after := ""
	total := 0
	for {
		preloadLimiter.wait()
		members, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			log.Printf("警告: ギルド(%s)のメンバー取得に失敗しました: %v", guildID, err)
			return
		}
		if len(members) == 0 {
			break
		}
		for _, m := range members {
			if m.User == nil {
				continue
			}
			saveUserData(m.User)
			entry := Member{
				Operation:                  MemberPrevious,
				UserID:                     m.User.ID,
				Nickname:                   m.Nick,
				Roles:                      m.Roles,
				JoinedAt:                   m.JoinedAt,
				CommunicationDisabledUntil: m.CommunicationDisabledUntil,
			}
			if err := appendMemberLog(guildID, entry); err != nil {
				log.Printf("警告: メンバー(%s)のプリロード保存に失敗しました: %v", m.User.ID, err)
			}
			total++
		}
		after = members[len(members)-1].User.ID
		if len(members) < 1000 {
			break
		}
	}
	fmt.Printf("[プリロード] ギルド(%s) のメンバー %d 件を記録しました\n", guildID, total)
}

// preloadChannels はギルド内の全テキストチャンネル(スレッド含む)を取得し、
// 設定を保存した上でメッセージ履歴のプリロードを行う。
func preloadChannels(s *discordgo.Session, guildID string, until time.Time) {
	preloadLimiter.wait()
	channels, err := s.GuildChannels(guildID)
	if err != nil {
		log.Printf("警告: ギルド(%s)のチャンネル取得に失敗しました: %v", guildID, err)
		return
	}
	for _, c := range channels {
		if !isTextLikeChannel(c) {
			continue
		}
		saveChannelDataFromChannel(guildID, c)
		preloadMessages(s, guildID, c.ID, until)
	}
}

func isTextLikeChannel(c *discordgo.Channel) bool {
	switch c.Type {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildPublicThread,
		discordgo.ChannelTypeGuildPrivateThread,
		discordgo.ChannelTypeGuildNewsThread:
		return true
	default:
		return false
	}
}

// preloadMessages は指定チャンネルのメッセージを新しい方から100件ずつ遡って取得し、
// until より古いメッセージに到達するまで保存を続ける。
func preloadMessages(s *discordgo.Session, guildID, channelID string, until time.Time) {
	beforeID := ""
	total := 0
	for {
		preloadLimiter.wait()
		msgs, err := s.ChannelMessages(channelID, 100, beforeID, "", "")
		if err != nil {
			log.Printf("警告: チャンネル(%s)のメッセージ取得に失敗しました: %v", channelID, err)
			return
		}
		if len(msgs) == 0 {
			break
		}

		reachedUntil := false
		for _, dm := range msgs {
			if dm.Timestamp.Before(until) {
				reachedUntil = true
				break
			}
			saveMessage(s, guildID, channelID, MessageOperationCreate, dm)
			total++
		}

		beforeID = msgs[len(msgs)-1].ID
		if reachedUntil || len(msgs) < 100 {
			break
		}
	}
	if total > 0 {
		fmt.Printf("[プリロード] チャンネル(%s) のメッセージ %d 件を記録しました\n", channelID, total)
	}
}

// ==================== メッセージ作成 ====================

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}

	fmt.Printf("[新規メッセージ] %s: %s\n", m.Author.Username, m.Content)

	// --- ユーザー情報の差分チェック・保存 ---
	saveUserData(m.Author)

	// --- サーバー情報の差分チェック・保存 (DMの場合はGuildIDが空) ---
	if m.GuildID != "" {
		saveGuildData(s, m.GuildID)
		saveChannelData(s, m.GuildID, m.ChannelID)
		// メンバー情報(ニックネーム・ロール)の差分チェック
		if m.Member != nil {
			saveMemberData(m.GuildID, m.Author.ID, m.Member)
		}
	}

	// --- メッセージ本体・添付ファイルの保存 ---
	saveMessage(s, m.GuildID, m.ChannelID, MessageOperationCreate, m.Message)

	if m.Content == "ぴん" {
		s.ChannelMessageSend(m.ChannelID, "ぽん！")
	}
}

// ==================== メッセージ編集 ====================

func onMessageEdit(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}

	fmt.Printf("[メッセージ編集] %s が変更: %s\n", m.Author.Username, m.Content)

	saveMessage(s, m.GuildID, m.ChannelID, MessageOperationEdit, m.Message)
}

// ==================== メッセージ削除 ====================

func onMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	fmt.Printf("[メッセージ削除] ID: %s がチャンネル(ID: %s)から削除されました\n", m.ID, m.ChannelID)

	msg := Message{
		Operation: MessageOperationDelete,
		ID:        m.ID,
		AuthorID:  "",
		Timestamp: nowUTC(),
	}
	dir := channelMessagesDir(m.GuildID, m.ChannelID)
	if err := appendMessageLog(dir, msg); err != nil {
		log.Printf("警告: 削除メッセージのログ保存に失敗しました: %v", err)
	}
}

// ==================== ユーザー情報の保存 ====================

// onUserUpdate はBot自身も含む「見えている」ユーザーの情報が更新された際に発火する。
// メッセージ送信を待たずにユーザー設定の変化を捕捉するために利用する。
func onUserUpdate(s *discordgo.Session, u *discordgo.UserUpdate) {
	if u.User == nil {
		return
	}
	saveUserData(u.User)
}

func saveUserData(author *discordgo.User) {
	current := User{
		ID:            author.ID,
		Username:      author.Username,
		GlobalName:    author.GlobalName,
		DisplayName:   author.GlobalName,
		Discriminator: author.Discriminator,
		Avatar:        author.Avatar,
	}

	changed, err := appendHistoryIfChanged(userConfigsDir(author.ID), current)
	if err != nil {
		log.Printf("警告: ユーザー(%s)の設定保存に失敗しました: %v", author.ID, err)
	} else if changed {
		fmt.Printf("[ユーザー設定更新] %s の設定を保存しました\n", author.ID)
	}

	if author.Avatar != "" {
		avatarURL := discordgo.EndpointUserAvatar(author.ID, author.Avatar)
		iconChanged, err := saveIconIfChanged(userIconsDir(author.ID), author.Avatar, avatarURL)
		if err != nil {
			log.Printf("警告: ユーザー(%s)のアイコン保存に失敗しました: %v", author.ID, err)
		} else if iconChanged {
			fmt.Printf("[ユーザーアイコン更新] %s のアイコンを保存しました\n", author.ID)
		}
	}
}

// ==================== ギルド情報の保存 ====================

// onGuildUpdate はサーバー名・アイコン等がメッセージ投稿を伴わずに変更された場合にも捕捉する。
func onGuildUpdate(s *discordgo.Session, g *discordgo.GuildUpdate) {
	if g.Guild == nil {
		return
	}
	saveGuildDataFromGuild(g.Guild)
}

func saveGuildData(s *discordgo.Session, guildID string) {
	guild, err := s.State.Guild(guildID)
	if err != nil || guild == nil {
		guild, err = s.Guild(guildID)
		if err != nil {
			log.Printf("警告: ギルド(%s)の取得に失敗しました: %v", guildID, err)
			return
		}
	}
	saveGuildDataFromGuild(guild)
}

func saveGuildDataFromGuild(guild *discordgo.Guild) {
	current := Guild{
		ID:              guild.ID,
		Name:            guild.Name,
		Description:     guild.Description,
		Icon:            guild.Icon,
		Banner:          guild.Banner,
		Splash:          guild.Splash,
		DiscoverySplash: guild.DiscoverySplash,
	}

	changed, err := appendHistoryIfChanged(guildConfigsDir(guild.ID), current)
	if err != nil {
		log.Printf("警告: ギルド(%s)の設定保存に失敗しました: %v", guild.ID, err)
	} else if changed {
		fmt.Printf("[ギルド設定更新] %s の設定を保存しました\n", guild.ID)
	}

	if guild.Icon != "" {
		iconURL := discordgo.EndpointGuildIcon(guild.ID, guild.Icon)
		iconChanged, err := saveIconIfChanged(guildIconsDir(guild.ID), guild.Icon, iconURL)
		if err != nil {
			log.Printf("警告: ギルド(%s)のアイコン保存に失敗しました: %v", guild.ID, err)
		} else if iconChanged {
			fmt.Printf("[ギルドアイコン更新] %s のアイコンを保存しました\n", guild.ID)
		}
	}

	// カスタム絵文字の差分チェック(新規追加分のみ保存)
	saveGuildEmojis(guild.ID, guild.Emojis)
}

func saveGuildEmojis(guildID string, emojis []*discordgo.Emoji) {
	dir := guildEmojisDir(guildID)
	for _, e := range emojis {
		if e.ID == "" {
			continue
		}
		// 既に同一IDの絵文字ファイルが存在するかチェック
		matches, _ := filepath.Glob(filepath.Join(dir, "*__"+e.Name+".png"))
		matchesAnimated, _ := filepath.Glob(filepath.Join(dir, "*__"+e.Name+".gif"))
		if len(matches) > 0 || len(matchesAnimated) > 0 {
			continue
		}
		ext := ".png"
		if e.Animated {
			ext = ".gif"
		}
		url := discordgo.EndpointEmoji(e.ID)
		if e.Animated {
			url = discordgo.EndpointEmojiAnimated(e.ID)
		}
		if err := ensureDir(dir); err != nil {
			log.Printf("警告: 絵文字ディレクトリ作成に失敗しました: %v", err)
			continue
		}
		filename := timestampName() + "__" + e.Name + ext
		if err := downloadFile(url, filepath.Join(dir, filename)); err != nil {
			log.Printf("警告: 絵文字(%s)のダウンロードに失敗しました: %v", e.Name, err)
			continue
		}
		fmt.Printf("[絵文字追加] %s を保存しました\n", e.Name)
	}
}

// ==================== チャンネル情報の保存 ====================
//
// 名前・トピック・NSFW設定・種別など、表のリストには明記されていない変更も
// Channel構造体に含まれる範囲で差分チェックして記録する。

// onChannelUpdate はチャンネル名やトピックがメッセージ投稿を伴わずに変更された場合にも捕捉する。
func onChannelUpdate(s *discordgo.Session, c *discordgo.ChannelUpdate) {
	if c.Channel == nil || c.GuildID == "" {
		return
	}
	saveChannelDataFromChannel(c.GuildID, c.Channel)
}

func saveChannelData(s *discordgo.Session, guildID, channelID string) {
	channel, err := s.State.Channel(channelID)
	if err != nil || channel == nil {
		channel, err = s.Channel(channelID)
		if err != nil {
			log.Printf("警告: チャンネル(%s)の取得に失敗しました: %v", channelID, err)
			return
		}
	}
	saveChannelDataFromChannel(guildID, channel)
}

func saveChannelDataFromChannel(guildID string, channel *discordgo.Channel) {
	current := Channel{
		ID:       channel.ID,
		Name:     channel.Name,
		Topic:    channel.Topic,
		IsThread: channel.IsThread(),
	}

	changed, err := appendHistoryIfChanged(channelConfigsDir(guildID, channel.ID), current)
	if err != nil {
		log.Printf("警告: チャンネル(%s)の設定保存に失敗しました: %v", channel.ID, err)
	} else if changed {
		fmt.Printf("[チャンネル設定更新] %s の設定を保存しました\n", channel.ID)
	}
}

// ==================== メンバー情報の保存 ====================

// onGuildMemberAdd はメンバーの参加を検出した時点でJoinとして記録する。
func onGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m.Member == nil || m.User == nil {
		return
	}
	saveUserData(m.User)

	entry := Member{
		Operation: MemberJoin,
		UserID:    m.User.ID,
		Nickname:  m.Nick,
		Roles:     m.Roles,
		JoinedAt:  m.JoinedAt,
	}
	if err := appendMemberLog(m.GuildID, entry); err != nil {
		log.Printf("警告: メンバー参加(%s)のログ保存に失敗しました: %v", m.User.ID, err)
		return
	}
	fmt.Printf("[メンバー参加] %s がサーバー(%s)に参加しました\n", m.User.ID, m.GuildID)
}

// onGuildMemberRemove はメンバーの離脱(またはキック/BAN)を検出した時点でLeaveとして記録する。
func onGuildMemberRemove(s *discordgo.Session, m *discordgo.GuildMemberRemove) {
	if m.User == nil {
		return
	}

	entry := Member{
		Operation: MemberLeave,
		UserID:    m.User.ID,
	}
	if m.Member != nil {
		entry.Nickname = m.Nick
		entry.Roles = m.Roles
	}
	if err := appendMemberLog(m.GuildID, entry); err != nil {
		log.Printf("警告: メンバー離脱(%s)のログ保存に失敗しました: %v", m.User.ID, err)
		return
	}
	fmt.Printf("[メンバー離脱] %s がサーバー(%s)から離脱しました\n", m.User.ID, m.GuildID)
}

// onGuildMemberUpdate はニックネーム・ロールなどの変更をメッセージ投稿を伴わずに捕捉する。
func onGuildMemberUpdate(s *discordgo.Session, m *discordgo.GuildMemberUpdate) {
	if m.Member == nil || m.User == nil {
		return
	}
	saveUserData(m.User)
	saveMemberData(m.GuildID, m.User.ID, m.Member)
}

func saveMemberData(guildID, userID string, member *discordgo.Member) {
	current := Member{
		Operation: MemberUpdate,
		UserID:    userID,
		Nickname:  member.Nick,
		Roles:     member.Roles,
	}
	if !member.JoinedAt.IsZero() {
		current.JoinedAt = member.JoinedAt
	}
	if member.CommunicationDisabledUntil != nil {
		current.CommunicationDisabledUntil = member.CommunicationDisabledUntil
	}

	// 直近保存したメンバー情報(operationは無視して比較)と差分があれば記録する
	changed, latest := memberChanged(guildID, userID, current)
	if !changed {
		return
	}
	if latest == nil {
		current.Operation = MemberJoin
	}

	if err := appendMemberLog(guildID, current); err != nil {
		log.Printf("警告: メンバー(%s)のログ保存に失敗しました: %v", userID, err)
		return
	}
	fmt.Printf("[メンバー更新] %s の情報を記録しました (%s)\n", userID, current.Operation)
}

// memberChanged は当日以前の全メンバーログを走査し、対象ユーザーの最新レコード(Leave以降は無視)と
// current を比較する。変化があれば true, 直前レコードが存在しなければ (true, nil) を返す。
func memberChanged(guildID, userID string, current Member) (bool, *Member) {
	dir := guildMembersDir(guildID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true, nil
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	// 新しい日付から走査
	for i := len(files) - 1; i >= 0; i-- {
		var records []Member
		data, err := os.ReadFile(filepath.Join(dir, files[i]))
		if err != nil {
			continue
		}
		if err := unmarshalJSON(data, &records); err != nil {
			continue
		}
		for j := len(records) - 1; j >= 0; j-- {
			if records[j].UserID != userID {
				continue
			}
			last := records[j]
			if last.Operation == MemberLeave {
				// 離脱後の再参加は必ずJoinとして扱う
				return true, nil
			}
			if last.Nickname == current.Nickname && stringSlicesEqual(last.Roles, current.Roles) {
				return false, &last
			}
			return true, &last
		}
	}
	return true, nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int)
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, c := range seen {
		if c != 0 {
			return false
		}
	}
	return true
}

// ==================== メッセージ・添付ファイルの保存 ====================

func saveMessage(s *discordgo.Session, guildID, channelID string, op MessageOperation, dm *discordgo.Message) {
	if dm == nil {
		return
	}

	attachmentsDir := channelAttachmentsDir(guildID, channelID)
	var savedAttachments []Attachment
	for _, a := range dm.Attachments {
		localName := timestampName() + "__" + a.Filename
		localPath := filepath.Join(attachmentsDir, localName)
		if err := ensureDir(attachmentsDir); err != nil {
			log.Printf("警告: 添付ファイルディレクトリ作成に失敗しました: %v", err)
		} else if err := downloadFile(a.URL, localPath); err != nil {
			log.Printf("警告: 添付ファイル(%s)のダウンロードに失敗しました: %v", a.Filename, err)
			localPath = ""
		}
		savedAttachments = append(savedAttachments, Attachment{
			Filename:  a.Filename,
			URL:       a.URL,
			LocalPath: localPath,
			Size:      int64(a.Size),
		})
	}

	var embeds []discordgo.MessageEmbed
	for _, e := range dm.Embeds {
		if e != nil {
			embeds = append(embeds, *e)
		}
	}

	msg := Message{
		Operation:   op,
		ID:          dm.ID,
		AuthorID:    authorIDOf(dm),
		Content:     dm.Content,
		Embeds:      embeds,
		Attachments: savedAttachments,
		Timestamp:   timestampOrNow(dm),
	}
	if op == MessageOperationEdit && dm.EditedTimestamp != nil {
		t := *dm.EditedTimestamp
		msg.EditedTimestamp = &t
	}

	dir := channelMessagesDir(guildID, channelID)
	if err := appendMessageLog(dir, msg); err != nil {
		log.Printf("警告: メッセージ(%s)のログ保存に失敗しました: %v", dm.ID, err)
	}
}

func authorIDOf(dm *discordgo.Message) string {
	if dm.Author != nil {
		return dm.Author.ID
	}
	return ""
}

func timestampOrNow(dm *discordgo.Message) time.Time {
	if !dm.Timestamp.IsZero() {
		return dm.Timestamp
	}
	return nowUTC()
}
