# discord-audit

Discord サーバー(Bot 参加先)の各種情報・アクティビティを、変化があった時だけ差分でローカルに記録し続ける監査ロガー。

---

## 環境

`go version go1.26.4 linux/amd64`

## Bot の推奨権限

- Channel View
- Message Read History

## 実行

```bash
# 単一Botトークンの場合
export DISCORD_TOKEN="<your_bot_token>"
go run .

# 複数Botトークンの場合
export DISCORD_TOKEN="<token1>,<token2>"
go run .

# 過去データも取得する場合(yyyymmddまで遡る)
go run . --preload=20240101
```

## 保存されるファイル構造

| `filename`                                                                              | Description                                                                    |
| :-------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------- |
| ./user/                                                                                 | すべてのユーザーについて保存されているフォルダー                               |
| ./user/`<user_id>`/                                                                     | `user_id` に基づいてデータが入っている                                         |
| ./user/`<user_id>`/configs/                                                             | ユーザー名・タグ・ステータスなどの歴代の設定データ                             |
| ./user/`<user_id>`/configs/`yyyymmdd_hhmmss.json`                                       | 変更が検出された時点の設定データ                                               |
| ./user/`<user_id>`/icons/                                                               | 歴代のアイコンデータ                                                           |
| ./user/`<user_id>`/icons/`yyyymmdd_hhmmss.png`                                          | 変更が検出された時点のデータ                                                   |
| ./guilds/                                                                               | すべてのサーバーについて保存されているフォルダー                               |
| ./guilds/`<guild_id>`/                                                                  | `guild_id` に基づいてデータが入っている                                        |
| ./guilds/`<guild_id>`/configs/                                                          | サーバー名などの歴代の設定データ                                               |
| ./guilds/`<guild_id>`/configs/`yyyymmdd_hhmmss.json`                                    | 変更が検出された時点の設定データ                                               |
| ./guilds/`<guild_id>`/icons/                                                            | 歴代のアイコンデータ                                                           |
| ./guilds/`<guild_id>`/icons/`yyyymmdd_hhmmss.png`                                       | 変更が検出された時点のデータ                                                   |
| ./guilds/`<guild_id>`/emojis/                                                           | サーバーのカスタム絵文字データ                                                 |
| ./guilds/`<guild_id>`/emojis/`yyyymmdd_hhmmss__<emojiname>.png`                         | 追加または変更が検出された時点の絵文字データ                                   |
| ./guilds/`<guild_id>`/members/                                                          | サーバーに参加している(または過去にいた)メンバーの記録データ                   |
| ./guilds/`<guild_id>`/members/`yyyymmdd.json`                                           | 指定された日付のメンバーの参加・離脱・変更などのログデータ                     |
| ./guilds/`<guild_id>`/channels/                                                         | 各チャンネルデータ                                                             |
| ./guilds/`<guild_id>`/channels/`<channel_id>`/                                          | `channel_id` に基づいてデータが入っている                                      |
| ./guilds/`<guild_id>`/channels/`<channel_id>`/attachments/                              | チャンネル内で投稿された添付ファイルのフォルダー                               |
| ./guilds/`<guild_id>`/channels/`<channel_id>`/attachments/`yyyymmdd_hhmmss__<filename>` | 投稿または検出された時点の添付ファイルデータ                                   |
| ./guilds/`<guild_id>`/channels/`<channel_id>`/messages                                  | 日付ごとのメッセージデータ                                                     |
| ./guilds/`<guild_id>`/channels/`<channel_id>`/messages/`yyyymmdd.json`                  | 指定された日付のメッセージデータ(`AuthorID` は `Delete` の場合は削除実行者 ID) |
