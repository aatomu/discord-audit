package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"
)

func init() {
	// user dir
	os.MkdirAll("./users", 0750)
	// guilds dir
	os.MkdirAll("./guilds", 0750)
}
func main() {
	// 1. 環境変数からBotのトークンを取得
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("環境変数 DISCORD_TOKEN が設定されていません")
	}

	// 2. Discordのセッションを作成 ("Bot " プレフィックスが必要です)
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Discordセッションの作成に失敗しました: %v", err)
	}

	// 3. インテント（Botが受け取るイベントの権限）を設定
	// メッセージ内容を読み取るには、Discord Developer Portalでの「Message Content Intent」の有効化も必要です
	dg.Identify.Intents =
		discordgo.IntentsGuildMessages |
			discordgo.IntentsDirectMessages |
			discordgo.IntentMessageContent

	// 4. 各イベントハンドラー（リスナー）を登録
	dg.AddHandler(onMessageCreate)
	dg.AddHandler(onMessageEdit)
	dg.AddHandler(onMessageDelete)

	// 5. Discordへの接続を開始
	err = dg.Open()
	if err != nil {
		log.Fatalf("Discordへの接続に失敗しました: %v", err)
	}
	defer dg.Close()

	fmt.Println("Botが起動しました。Ctrl+C で終了します。")

	// シグナルを待機して、プログラムがすぐに終了するのを防ぐ
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}

// --- イベントハンドラーの実装 ---

// メッセージが送信された時
func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Bot自身のメッセージは無視する（無限ループ防止）
	if m.Author.ID == s.State.User.ID {
		return
	}

	fmt.Printf("[新規メッセージ] %s: %s\n", m.Author.Username, m.Content)

	// 簡単な応答の例
	if m.Content == "ぴん" {
		s.ChannelMessageSend(m.ChannelID, "ぽん！")
	}
}

// メッセージが編集された時
func onMessageEdit(s *discordgo.Session, m *discordgo.MessageUpdate) {
	// 編集イベントは、埋め込み(Embed)が追加されただけの場合など、
	// AuthorやContentが空の状態で飛んでくることがあるため、安全チェックを入れます
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}

	fmt.Printf("[メッセージ編集] %s が変更: %s\n", m.Author.Username, m.Content)
}

// メッセージが削除された時
func onMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	// 削除されたメッセージは、デフォルトの状態では「メッセージID」と「チャンネルID」しか取得できません
	// （過去のメッセージ本文をBotのメモリ内にキャッシュしていない限り、内容の取得はできません）
	fmt.Printf("[メッセージ削除] ID: %s がチャンネル(ID: %s)から削除されました\n", m.ID, m.ChannelID)
}
