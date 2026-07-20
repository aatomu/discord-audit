# discord-audit
discord bot の取得できる各種情報を記録する。

## 環境
`go version go1.26.4 linux/amd64`

## 実行
```bash
# Set environment your bot tokens
export DISCORD_TOKEN="<your_bot_token>"
# Run for audit discord bot
go run .
```

## Botの推奨権限
- Channel View
- Message Read History

## 保存されるファイル構造
|`filename`|Description|
|:-|:-|
|./user/| すべてのユーザーについて保存されているフォルダー|
|./user/`<user_id>`/|`user_id` に基づいてデータが入っている|
|./user/`<user_id>`/configs/ | ユーザー名・タグ・ステータスなどの歴代の設定データ|
|./user/`<user_id>`/configs/`yyyymmdd_hhmmss.json` | 変更が検出された時点の設定データ |
|./user/`<user_id>`/icons/ | 歴代のアイコンデータ|
|./user/`<user_id>`/icons/`yyyymmdd_hhmmss.png` | 変更が検出された時点のデータ |
|./guilds/| すべてのサーバーについて保存されているフォルダー|
|./guilds/`<guild_id>`/|`guild_id` に基づいてデータが入っている|
|./guilds/`<guild_id>`/configs/ | サーバー名などの歴代の設定データ|
|./guilds/`<guild_id>`/configs/`yyyymmdd_hhmmss.json` | 変更が検出された時点の設定データ |
|./guilds/`<guild_id>`/icons/ | 歴代のアイコンデータ|
|./guilds/`<guild_id>`/icons/`yyyymmdd_hhmmss.png` | 変更が検出された時点のデータ |
|./guilds/`<guild_id>`/emojis/ | サーバーのカスタム絵文字データ |
|./guilds/`<guild_id>`/emojis/`yyyymmdd_hhmmss__<emojiname>.png` | 追加または変更が検出された時点の絵文字データ |
|./guilds/`<guild_id>`/members/ | サーバーに参加している（または過去にいた）メンバーの記録データ |
|./guilds/`<guild_id>`/members/`yyyymmdd.json` | 指定された日付のメンバーの参加・離脱・変更などのログデータ |
|./guilds/`<guild_id>`/channels/ | 各チャンネルデータ|
|./guilds/`<guild_id>`/channels/`<channel_id>`/ | `channel_id` に基づいてデータが入っている |
|./guilds/`<guild_id>`/channels/`<channel_id>`/attachments/ | チャンネル内で投稿された添付ファイルのフォルダー |
|./guilds/`<guild_id>`/channels/`<channel_id>`/attachments/`yyyymmdd_hhmmss__<filename>` | 投稿または検出された時点の添付ファイルデータ |
|./guilds/`<guild_id>`/channels/`<channel_id>`/messages | 日付ごとのメッセージデータ |
|./guilds/`<guild_id>`/channels/`<channel_id>`/messages/`yyyymmdd.json` | 指定された日付のメッセージデータ |