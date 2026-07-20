package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

func nowUTC() time.Time {
	return time.Now().UTC()
}

func init() {
	os.MkdirAll("./user", 0750)
	os.MkdirAll("./guilds", 0750)
}

// ==================== 複数Bot(マルチトークン)対応 ====================
//
// DISCORD_TOKEN は "token1,token2,token3" のようにカンマ区切りで複数指定できる。
// Botごとに閲覧できるチャンネルが異なる(権限の違いで見えるBot/見えないBotがある)ため、
// 「ギルド単位でどれか1つのBotに専任させる」方式は取りこぼしの原因になる。
// そのため各Botは自分が見えている範囲を独立に処理しつつ、同一イベントを複数のBotが
// 重複して受信した場合にのみ、短期間(dedupTTL)のイベントキャッシュで二重記録を防ぐ。
// (= キャッシュに無ければ処理する、あればスキップする 方式)

type botSession struct {
	session *discordgo.Session
	label   string       // ログ表示用の識別子("bot1(1234567890)"のような形式)
	limiter *rateLimiter // トークンごとに独立したプリロード用レートリミッター
}

var (
	sessionLabelMu sync.RWMutex
	sessionLabel   = map[*discordgo.Session]string{} // *discordgo.Session -> label
)

func labelOf(s *discordgo.Session) string {
	sessionLabelMu.RLock()
	defer sessionLabelMu.RUnlock()
	if l, ok := sessionLabel[s]; ok {
		return l
	}
	return "unknown"
}

// parseTokens は DISCORD_TOKEN の値を ',' で分割し、前後の空白除去・重複除去を行う。
func parseTokens(raw string) []string {
	seen := map[string]bool{}
	var tokens []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tokens = append(tokens, t)
	}
	return tokens
}

// ==================== イベント重複排除キャッシュ ====================
//
// 複数Botが同じギルド/チャンネルを見えている場合、同一イベントが複数Botから
// ほぼ同時に届くことがある。キー(イベントの種類+対象ID、必要に応じて内容のハッシュ)を
// dedupTTLの間だけ記憶しておき、既に見たキーであれば処理をスキップする。

const dedupTTL = 60 * time.Second

type dedupCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newDedupCache() *dedupCache {
	return &dedupCache{seen: map[string]time.Time{}}
}

// shouldProcess はkeyがttl以内に処理済みでなければtrueを返し、処理済みとして記録する。
// 既にttl以内に処理済みであればfalseを返す(呼び出し側はスキップする)。
func (d *dedupCache) shouldProcess(key string, ttl time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if t, ok := d.seen[key]; ok && now.Sub(t) < ttl {
		return false
	}
	d.seen[key] = now
	if len(d.seen) > 5000 {
		cutoff := now.Add(-ttl)
		for k, v := range d.seen {
			if v.Before(cutoff) {
				delete(d.seen, k)
			}
		}
	}
	return true
}

var eventDedup = newDedupCache()

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

	rawTokens := os.Getenv("DISCORD_TOKEN")
	if rawTokens == "" {
		log.Fatal("環境変数 DISCORD_TOKEN が設定されていません")
	}
	tokens := parseTokens(rawTokens)
	if len(tokens) == 0 {
		log.Fatal("環境変数 DISCORD_TOKEN に有効なトークンがありません")
	}

	var sessions []*botSession
	for i, token := range tokens {
		label := fmt.Sprintf("bot%d(-)", i+1)

		dg, err := discordgo.New("Bot " + token)
		if err != nil {
			log.Printf("[%s] Discordセッションの作成に失敗しました: %v\n", label, err)
			continue
		}

		dg.Identify.Intents =
			discordgo.IntentsGuilds |
				discordgo.IntentsGuildMessages |
				discordgo.IntentsDirectMessages |
				discordgo.IntentMessageContent |
				discordgo.IntentsGuildEmojis

		sessionLabelMu.Lock()
		sessionLabel[dg] = label
		sessionLabelMu.Unlock()

		dg.AddHandler(onMessageCreate)
		dg.AddHandler(onMessageEdit)
		dg.AddHandler(onMessageDelete)
		dg.AddHandler(onGuildMemberAdd)
		dg.AddHandler(onGuildMemberRemove)
		dg.AddHandler(onGuildMemberUpdate)
		dg.AddHandler(onChannelUpdate)
		dg.AddHandler(onGuildUpdate)
		dg.AddHandler(onUserUpdate)

		bs := &botSession{session: dg, label: label, limiter: newRateLimiter(20)}
		sessions = append(sessions, bs)

		// Readyイベント受信後(ギルド一覧がStateに載った後)にプリロードを1度だけ実行する。
		var once bool
		dg.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
			bs.label = fmt.Sprintf("%s(%s)", r.User.Username, r.User.ID)
			sessionLabelMu.Lock()
			sessionLabel[dg] = bs.label
			sessionLabelMu.Unlock()

			Log(SystemUser, LevelInfo, Ctx().Bot(bs.label), "bot has on Ready.")

			if preloadUntil != nil {
				if once {
					return
				}
				once = true
				go runPreload(bs, *preloadUntil)
			}
		})
	}

	for _, bs := range sessions {
		if err := bs.session.Open(); err != nil {
			log.Printf("[%s] Discordへの接続に失敗しました: %v\n", bs.label, err)
		}
	}
	defer func() {
		for _, bs := range sessions {
			bs.session.Close()
		}
	}()

	log.Printf("Botの起動を呼び出ししました(%d個のToken)。Ctrl+C で終了します。", len(sessions))

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}

// ==================== プリロード(過去データの一括取得) ====================
//
// --preload=yyyymmdd を指定した場合、Bot起動時にその日まで遡って
// 全ギルドの設定・メンバー一覧・全チャンネルのメッセージ履歴を取得して保存する。
// Discordのレート制限(概ね50 req/秒)の半分以下(20 req/秒)で処理する。

func runPreload(bs *botSession, until time.Time) {
	s := bs.session
	gctxAll := Ctx().Bot(bs.label)
	Log(SystemPreload, LevelInfo, gctxAll, "%s まで遡ってデータを取得します", until.Format("2006-01-02"))

	guilds := s.State.Guilds
	targetGuilds := len(guilds)
	for i, g := range guilds {
		guildNo := i + 1
		gctx := Ctx().Bot(bs.label).Guild(g.Name, g.ID)

		Log(SystemPreload, LevelInfo, gctx, "No.%d/%d 処理を開始します", guildNo, targetGuilds)

		bs.limiter.wait()
		full, err := s.Guild(g.ID)
		if err != nil {
			Log(SystemPreload, LevelWarn, gctx, "No.%d ギルドの取得に失敗しました: %v", guildNo, err)
			continue
		}

		saveGuildDataFromGuild(full)
		preloadMembers(bs, guildNo, full.Name, full.ID)
		preloadChannels(bs, guildNo, full.Name, full.ID, until)

		Log(SystemPreload, LevelInfo, gctx, "No.%d 処理が完了しました", guildNo)
	}

	Log(SystemPreload, LevelInfo, gctxAll, "すべてのギルドの処理が完了しました")
}

// preloadMembers はギルドの全メンバーをページングで取得し、Previous操作として記録する。
func preloadMembers(bs *botSession, guildNo int, guildName, guildID string) {
	s := bs.session
	gctx := Ctx().Bot(bs.label).Guild(guildName, guildID)
	after := ""
	total := 0
	for {
		bs.limiter.wait()
		members, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			Log(SystemPreload, LevelWarn, gctx, "No.%d メンバーの取得に失敗しました: %v", guildNo, err)
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
			key := fmt.Sprintf("member:previous:%s:%s", guildID, m.User.ID)
			if !eventDedup.shouldProcess(key, dedupTTL) {
				continue
			}
			entry := Member{
				Operation:                  MemberPrevious,
				UserID:                     m.User.ID,
				Nickname:                   m.Nick,
				Roles:                      m.Roles,
				JoinedAt:                   m.JoinedAt,
				CommunicationDisabledUntil: m.CommunicationDisabledUntil,
			}
			if err := appendMemberLog(guildID, entry); err != nil {
				Log(SystemPreload, LevelWarn, Ctx().Bot(bs.label).Guild(guildName, guildID).User(m.User.Username, m.User.ID),
					"No.%d メンバーの保存に失敗しました: %v", guildNo, err)
			}
			total++
		}
		after = members[len(members)-1].User.ID
		if len(members) < 1000 {
			break
		}
	}
	Log(SystemPreload, LevelInfo, gctx, "No.%d メンバー: %d件を記録しました", guildNo, total)
}

// preloadChannels はギルド内の全テキストチャンネル(スレッド含む)を取得し、
// 設定を保存した上でメッセージ履歴のプリロードを行う。
func preloadChannels(bs *botSession, guildNo int, guildName, guildID string, until time.Time) {
	s := bs.session
	gctx := Ctx().Bot(bs.label).Guild(guildName, guildID)
	bs.limiter.wait()
	channels, err := s.GuildChannels(guildID)
	if err != nil {
		Log(SystemPreload, LevelWarn, gctx, "No.%d チャンネルの取得に失敗しました: %v", guildNo, err)
		return
	}
	for _, c := range channels {
		if !isTextLikeChannel(c) {
			continue
		}
		saveChannelDataFromChannel(guildID, c)
		preloadMessages(bs, guildNo, guildName, guildID, c.Name, c.ID, until)
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
func preloadMessages(bs *botSession, guildNo int, guildName, guildID, channelName, channelID string, until time.Time) {
	s := bs.session
	cctx := Ctx().Bot(bs.label).Guild(guildName, guildID).Channel(channelName, channelID)
	beforeID := ""
	total := 0
	for {
		bs.limiter.wait()
		msgs, err := s.ChannelMessages(channelID, 100, beforeID, "", "")
		if err != nil {
			Log(SystemPreload, LevelWarn, cctx, "No.%d メッセージ取得に失敗しました: %v", guildNo, err)
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
		Log(SystemPreload, LevelInfo, cctx, "No.%d メッセージ: %d件を記録しました", guildNo, total)
	}
}

// ==================== メッセージ作成 ====================

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}

	guild, _ := s.Guild(m.GuildID)
	guildName := "DM"
	if guild != nil {
		guildName = guild.Name
	}
	channel, _ := s.Channel(m.ChannelID)
	channelName := "unknown"
	if channel != nil {
		channelName = channel.Name
	}

	ctx := Ctx().Bot(labelOf(s)).Guild(guildName, m.GuildID).Channel(channelName, m.ChannelID).
		User(m.Author.Username, m.Author.ID).Message(m.Message.ID)
	Log(SystemMessage, LevelInfo, ctx, "Create: %s", strings.ReplaceAll(m.Content, "\n", "\\n"))

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

	guild, _ := s.Guild(m.GuildID)
	guildName := "DM"
	if guild != nil {
		guildName = guild.Name
	}
	channel, _ := s.Channel(m.ChannelID)
	channelName := "unknown"
	if channel != nil {
		channelName = channel.Name
	}

	ctx := Ctx().Bot(labelOf(s)).Guild(guildName, m.GuildID).Channel(channelName, m.ChannelID).
		User(m.Author.Username, m.Author.ID).Message(m.Message.ID)
	Log(SystemMessage, LevelInfo, ctx, "Edit: %s", strings.ReplaceAll(m.Content, "\n", "\\n"))

	saveMessage(s, m.GuildID, m.ChannelID, MessageOperationEdit, m.Message)
}

// ==================== メッセージ削除 ====================

func onMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	guild, _ := s.Guild(m.GuildID)
	guildName := "DM"
	if guild != nil {
		guildName = guild.Name
	}
	channel, _ := s.Channel(m.ChannelID)
	channelName := "unknown"
	if channel != nil {
		channelName = channel.Name
	}

	// 複数Botが同じチャンネルを閲覧できる場合の重複記録を防ぐ。
	key := fmt.Sprintf("message:%s:%s", MessageOperationDelete, m.ID)
	if !eventDedup.shouldProcess(key, dedupTTL) {
		return
	}

	ctx := Ctx().Bot(labelOf(s)).Guild(guildName, m.GuildID).Channel(channelName, m.ChannelID).Message(m.Message.ID)
	Log(SystemMessage, LevelInfo, ctx, "Delete")

	msg := Message{
		Operation: MessageOperationDelete,
		ID:        m.ID,
		AuthorID:  "",
		Timestamp: nowUTC(),
	}
	dir := channelMessagesDir(m.GuildID, m.ChannelID)
	if err := appendMessageLog(dir, msg); err != nil {
		Log(SystemMessage, LevelWarn, ctx, "削除メッセージのログ保存に失敗しました: %v", err)
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
	uctx := Ctx().User(author.Username, author.ID)

	current := User{
		ID:            author.ID,
		Username:      author.Username,
		GlobalName:    author.GlobalName,
		DisplayName:   author.GlobalName,
		Discriminator: author.Discriminator,
		Avatar:        author.Avatar,
	}

	changed, err := appendHistoryIfChanged(userDir(author.ID), current)
	if err != nil {
		Log(SystemUser, LevelError, uctx, "設定保存に失敗しました: %v", err)
	} else if changed {
		Log(SystemUser, LevelInfo, uctx, "設定を保存しました")
	}

	if author.Avatar != "" {
		avatarURL := discordgo.EndpointUserAvatar(author.ID, author.Avatar)
		iconChanged, err := saveIconIfChanged(userIconsDir(author.ID), author.Avatar, avatarURL)
		if err != nil {
			Log(SystemUser, LevelError, uctx, "アイコン保存に失敗しました: %v", err)
		} else if iconChanged {
			Log(SystemUser, LevelInfo, uctx, "アイコンを保存しました")
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
			Log(SystemGuild, LevelWarn, Ctx().Guild("", guildID), "取得に失敗しました: %v", err)
			return
		}
	}
	saveGuildDataFromGuild(guild)
}

func saveGuildDataFromGuild(guild *discordgo.Guild) {
	gctx := Ctx().Guild(guild.Name, guild.ID)

	current := Guild{
		ID:              guild.ID,
		Name:            guild.Name,
		Description:     guild.Description,
		Icon:            guild.Icon,
		Banner:          guild.Banner,
		Splash:          guild.Splash,
		DiscoverySplash: guild.DiscoverySplash,
	}

	changed, err := appendHistoryIfChanged(guildDir(guild.ID), current)
	if err != nil {
		Log(SystemGuild, LevelWarn, gctx, "設定保存に失敗しました: %v", err)
	} else if changed {
		Log(SystemGuild, LevelInfo, gctx, "設定を保存しました")
	}

	if guild.Icon != "" {
		iconURL := discordgo.EndpointGuildIcon(guild.ID, guild.Icon)
		iconChanged, err := saveIconIfChanged(guildIconsDir(guild.ID), guild.Icon, iconURL)
		if err != nil {
			Log(SystemGuild, LevelWarn, gctx, "アイコン保存に失敗しました: %v", err)
		} else if iconChanged {
			Log(SystemGuild, LevelInfo, gctx, "アイコンを保存しました")
		}
	}

	saveGuildEmojis(guild.ID, guild.Name, guild.Emojis)
}

func saveGuildEmojis(guildID, guildName string, emojis []*discordgo.Emoji) {
	dir := guildEmojisDir(guildID)
	for _, e := range emojis {
		if e.ID == "" {
			continue
		}
		ectx := Ctx().Guild(guildName, guildID).Emoji(e.Name, e.ID)

		matches, _ := filepath.Glob(filepath.Join(dir, "*__"+e.Name+".png"))
		matchesAnimated, _ := filepath.Glob(filepath.Join(dir, "*__"+e.Name+".gif"))
		if len(matches) > 0 || len(matchesAnimated) > 0 {
			continue
		}
		// 複数Botが同じ絵文字追加をほぼ同時に検出した場合の重複ダウンロードを防ぐ。
		if !eventDedup.shouldProcess(fmt.Sprintf("emoji:%s:%s", guildID, e.ID), dedupTTL) {
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
			Log(SystemEmoji, LevelWarn, ectx, "絵文字ディレクトリ作成に失敗しました: %v", err)
			continue
		}
		filename := timestampName() + "__" + e.Name + ext
		if err := downloadFile(url, filepath.Join(dir, filename)); err != nil {
			Log(SystemEmoji, LevelWarn, ectx, "ダウンロードに失敗しました: %v", err)
			continue
		}
		Log(SystemEmoji, LevelInfo, ectx, "絵文字を保存しました")
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
			Log(SystemChannel, LevelWarn, Ctx().Guild("", guildID).Channel("", channelID), "取得に失敗しました: %v", err)
			return
		}
	}
	saveChannelDataFromChannel(guildID, channel)
}

func saveChannelDataFromChannel(guildID string, channel *discordgo.Channel) {
	cctx := Ctx().Guild("", guildID).Channel(channel.Name, channel.ID)

	current := Channel{
		ID:       channel.ID,
		Name:     channel.Name,
		Topic:    channel.Topic,
		IsThread: channel.IsThread(),
	}

	changed, err := appendHistoryIfChanged(channelDir(guildID, channel.ID), current)
	if err != nil {
		Log(SystemChannel, LevelWarn, cctx, "設定保存に失敗しました: %v", err)
	} else if changed {
		Log(SystemChannel, LevelInfo, cctx, "設定を保存しました")
	}
}

// ==================== メンバー情報の保存 ====================

// onGuildMemberAdd はメンバーの参加を検出した時点でJoinとして記録する。
func onGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m.Member == nil || m.User == nil {
		return
	}
	saveUserData(m.User)

	// メンバー参加はギルド全体のイベントのため、同じギルドにいる複数Botが重複して受信し得る。
	if !eventDedup.shouldProcess(fmt.Sprintf("member:add:%s:%s", m.GuildID, m.User.ID), dedupTTL) {
		return
	}

	ctx := Ctx().Bot(labelOf(s)).Guild("", m.GuildID).User(m.User.Username, m.User.ID)

	entry := Member{
		Operation: MemberJoin,
		UserID:    m.User.ID,
		Nickname:  m.Nick,
		Roles:     m.Roles,
		JoinedAt:  m.JoinedAt,
	}
	if err := appendMemberLog(m.GuildID, entry); err != nil {
		Log(SystemUser, LevelWarn, ctx, "参加ログの保存に失敗しました: %v", err)
		return
	}
	Log(SystemUser, LevelInfo, ctx, "サーバーに参加しました")
}

// onGuildMemberRemove はメンバーの離脱(またはキック/BAN)を検出した時点でLeaveとして記録する。
func onGuildMemberRemove(s *discordgo.Session, m *discordgo.GuildMemberRemove) {
	if m.User == nil {
		return
	}
	if !eventDedup.shouldProcess(fmt.Sprintf("member:remove:%s:%s", m.GuildID, m.User.ID), dedupTTL) {
		return
	}

	ctx := Ctx().Bot(labelOf(s)).Guild("", m.GuildID).User(m.User.Username, m.User.ID)

	entry := Member{
		Operation: MemberLeave,
		UserID:    m.User.ID,
	}
	if m.Member != nil {
		entry.Nickname = m.Nick
		entry.Roles = m.Roles
	}
	if err := appendMemberLog(m.GuildID, entry); err != nil {
		Log(SystemUser, LevelWarn, ctx, "離脱ログの保存に失敗しました: %v", err)
		return
	}
	Log(SystemUser, LevelInfo, ctx, "サーバーから離脱しました")
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
	ctx := Ctx().Guild("", guildID).User("", userID)

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

	// 複数Botが同じメンバー変更をほぼ同時に検出した場合の重複記録を防ぐ。
	// (新しい状態の内容そのものをキーに含めることで、内容が異なる正当な連続更新は
	// 別イベントとして扱われる)
	dedupKey := fmt.Sprintf("member:update:%s:%s:%s:%s", guildID, userID, current.Nickname, strings.Join(current.Roles, ","))
	if !eventDedup.shouldProcess(dedupKey, dedupTTL) {
		return
	}

	// 直近保存したメンバー情報(operationは無視して比較)と差分があれば記録する
	changed, latest := memberChanged(guildID, userID, current)
	if !changed {
		return
	}
	if latest == nil {
		// GuildMemberAdd イベント経由ではなくメッセージ等から初めて検出した場合は、
		// Bot起動前から既に存在していたメンバーの可能性があるため Join ではなく
		// Previous として記録する(実際の参加は onGuildMemberAdd が担当する)。
		current.Operation = MemberPrevious
	}

	if err := appendMemberLog(guildID, current); err != nil {
		Log(SystemUser, LevelWarn, ctx, "ログ保存に失敗しました: %v", err)
		return
	}
	Log(SystemUser, LevelInfo, ctx, "情報を記録しました (%s)", current.Operation)
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
				// 離脱後の再検出はイベントとしての参加確認ができないため、
				// (true, nil) を返して呼び出し側に Previous として記録させる。
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

	// 複数Botが同じチャンネルを閲覧できる場合、同一メッセージのイベントが重複して
	// 届くことがあるため、(操作種別+メッセージID)単位で短期間だけ重複排除する。
	key := fmt.Sprintf("message:%s:%s", op, dm.ID)
	if !eventDedup.shouldProcess(key, dedupTTL) {
		return
	}

	attachmentsDir := channelAttachmentsDir(guildID, channelID)
	// 添付ファイルのファイル名プレフィックスは、ダウンロードした時刻ではなく
	// メッセージの作成日時(dm.Timestamp)を使う。これにより --preload 時も
	// 取得日ではなく本来の投稿日時を反映したファイル名になる。
	attachmentPrefix := timestampName()
	if !dm.Timestamp.IsZero() {
		attachmentPrefix = dm.Timestamp.UTC().Format("20060102_150405")
	}
	var savedAttachments []Attachment
	for i, a := range dm.Attachments {
		localName := fmt.Sprintf("%s_%02d__%s", attachmentPrefix, i+1, a.Filename)
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
