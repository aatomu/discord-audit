package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"
)

// ---------- パスヘルパー ----------

func userDir(userID string) string {
	return filepath.Join("user", userID)
}

func userIconsDir(userID string) string {
	return filepath.Join(userDir(userID), "icons")
}

func guildDir(guildID string) string {
	return filepath.Join("guilds", guildID)
}

func guildIconsDir(guildID string) string {
	return filepath.Join(guildDir(guildID), "icons")
}

func guildEmojisDir(guildID string) string {
	return filepath.Join(guildDir(guildID), "emojis")
}

func guildMembersDir(guildID string) string {
	return filepath.Join(guildDir(guildID), "members")
}

func channelDir(guildID, channelID string) string {
	return filepath.Join(guildDir(guildID), "channels", channelID)
}

func channelAttachmentsDir(guildID, channelID string) string {
	return filepath.Join(channelDir(guildID, channelID), "attachments")
}

func channelMessagesDir(guildID, channelID string) string {
	return filepath.Join(channelDir(guildID, channelID), "messages")
}

// ---------- 共通ユーティリティ ----------

func timestampName() string {
	return time.Now().Format("20060102_150405")
}

func dateName() string {
	return time.Now().Format("20060102")
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0750)
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ダウンロード失敗 (status %d): %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// unmarshalJSON は json.Unmarshal の薄いラッパー(他ファイルからの利用向け)。
func unmarshalJSON(data []byte, out interface{}) error {
	return json.Unmarshal(data, out)
}

// ---------- 設定(config)差分チェック: HistoryData方式 ----------
//
// 各エンティティ(ユーザー/サーバー/チャンネル)ごとに config.json という
// 単一ファイルを持ち、HistoryData{ Start, History []HistoryEntry } として蓄積する。
// 変化が検出された時だけ HistoryEntry{ RecordedAt, Data } を History に追記する。

func historyPath(dir string) string {
	return filepath.Join(dir, "config.json")
}

// loadHistoryData は dir/config.json を読み込む。存在しない場合は ok=false を返す。
func loadHistoryData[T any](dir string) (hd HistoryData[T], ok bool, err error) {
	data, err := os.ReadFile(historyPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return hd, false, nil
		}
		return hd, false, err
	}
	if err := json.Unmarshal(data, &hd); err != nil {
		return hd, false, err
	}
	return hd, true, nil
}

func writeHistoryData[T any](dir string, hd HistoryData[T]) error {
	if err := ensureDir(dir); err != nil {
		return err
	}
	b, err := json.MarshalIndent(hd, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(historyPath(dir), b, 0640)
}

// appendHistoryIfChanged は現在値と最後に記録された値(Historyの末尾、無ければStart)を比較し、
// 異なっていれば HistoryEntry を追記して保存する。初回(ファイル未作成)は Start として保存する。
// 変更があった(=書き込みが発生した)場合 true を返す。
func appendHistoryIfChanged[T any](dir string, current T) (bool, error) {
	hd, ok, err := loadHistoryData[T](dir)
	if err != nil {
		log.Printf("警告: %s の読み込みに失敗しました: %v", dir, err)
	}

	if !ok {
		hd = HistoryData[T]{Start: current}
		if err := writeHistoryData(dir, hd); err != nil {
			return false, err
		}
		return true, nil
	}

	var last T
	if len(hd.History) > 0 {
		last = hd.History[len(hd.History)-1].Data
	} else {
		last = hd.Start
	}

	if reflect.DeepEqual(last, current) {
		return false, nil
	}

	hd.History = append(hd.History, HistoryEntry[T]{RecordedAt: time.Now(), Data: current})
	if err := writeHistoryData(dir, hd); err != nil {
		return false, err
	}
	return true, nil
}

// ---------- アイコン/画像差分チェック共通処理 ----------

// saveIconIfChanged は現在の画像ハッシュ(hash: discordのavatar/iconハッシュ文字列)を
// 最後に保存したファイル名(拡張子抜きの末尾)と比較し、変化していればダウンロードして保存する。
// hash が空文字の場合は何もしない。
func saveIconIfChanged(dir string, hash string, downloadURL string) (bool, error) {
	if hash == "" {
		return false, nil
	}
	// 複数Botが同一の設定変更(アイコン変更)をほぼ同時に検出した場合の重複ダウンロードを防ぐ。
	key := fmt.Sprintf("icon:%s:%s", dir, hash)
	if !eventDedup.shouldProcess(key, dedupTTL) {
		return false, nil
	}
	lastHashPath := filepath.Join(dir, ".last_hash")
	lastHash, _ := os.ReadFile(lastHashPath)
	if string(lastHash) == hash {
		return false, nil
	}
	if err := ensureDir(dir); err != nil {
		return false, err
	}
	ext := ".png"
	if len(downloadURL) > 5 && downloadURL[len(downloadURL)-4:] == ".gif" {
		ext = ".gif"
	}
	filename := timestampName() + ext
	if err := downloadFile(downloadURL, filepath.Join(dir, filename)); err != nil {
		return false, err
	}
	if err := os.WriteFile(lastHashPath, []byte(hash), 0640); err != nil {
		log.Printf("警告: %s への最終ハッシュ書き込みに失敗しました: %v", lastHashPath, err)
	}
	return true, nil
}

// ---------- レートリミッター(プリロード用) ----------
//
// Discordのグローバルレート制限は概ね 50 req/秒。プリロード実行時はその半分以下、
// 20 req/秒 (= 50ms間隔) を上限とすることで、通常のBot動作を圧迫しないようにする。

type rateLimiter struct {
	mu          sync.Mutex
	last        time.Time
	minInterval time.Duration
}

func newRateLimiter(requestsPerSecond float64) *rateLimiter {
	return &rateLimiter{
		minInterval: time.Duration(float64(time.Second) / requestsPerSecond),
	}
}

// wait は前回の呼び出しから minInterval 以上経過するまでブロックする。
func (r *rateLimiter) wait() {
	r.mu.Lock()
	defer r.mu.Unlock()
	elapsed := time.Since(r.last)
	if elapsed < r.minInterval {
		time.Sleep(r.minInterval - elapsed)
	}
	r.last = time.Now()
}

// ---------- メッセージ日次ログの追記 ----------

// appendMessageLog は messages/yyyymmdd.json (JSON配列) に msg を追記する。
// ファイル名の日付は処理時点の日付ではなく、msg.Timestamp(メッセージの作成日時)を基準にする。
// これにより --preload で過去メッセージを取得した場合も、取得日ではなく本来の投稿日のファイルに保存される。
func appendMessageLog(dir string, msg Message) error {
	if err := ensureDir(dir); err != nil {
		return err
	}
	name := dateName()
	if !msg.Timestamp.IsZero() {
		name = msg.Timestamp.UTC().Format("20060102")
	}
	path := filepath.Join(dir, name+".json")

	var msgs []Message
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &msgs); err != nil {
			log.Printf("警告: %s のパースに失敗しました。上書きします: %v", path, err)
			msgs = nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	msgs = append(msgs, msg)

	// 常に作成日時(Timestamp)の昇順(古い順が先頭、新しい順が末尾)になるよう並べ替える。
	// 通常のイベント受信時は既に昇順で追記されるが、--preload 時は新しいメッセージから
	// 遡って取得するため、ソートしないと逆順になってしまう。
	sort.SliceStable(msgs, func(i, j int) bool {
		return msgs[i].Timestamp.Before(msgs[j].Timestamp)
	})

	b, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0640)
}

// ---------- メンバーログの追記 ----------

func appendMemberLog(guildID string, entry Member) error {
	dir := guildMembersDir(guildID)
	if err := ensureDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, dateName()+".json")

	var entries []Member
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &entries); err != nil {
			log.Printf("警告: %s のパースに失敗しました。上書きします: %v", path, err)
			entries = nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	entries = append(entries, entry)

	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0640)
}
