# claude-file-watch-hooks

English: [README.md](README.md)

Claude Codeで編集中のファイルを、別プロセス（自分の手動編集や別のツール）が
外部で書き換えていないか検知するhooks。Go製。

Gistはフォルダを持てないため、ファイル名で元のパスを表している
（`cmd_file_watch_track_main.go` → 本来は `go/cmd/file_watch_track/main.go`、
`internal_filewatch_common.go` → 本来は `go/internal/filewatch/common.go`）。
手元でビルドする場合はこの対応通りにフォルダへ配置し直してほしい。

## 何をするか

- `file_watch_track`(PostToolUse, matcher=Read): Readしたファイルをセッション単位でスナップショット保存。テキスト系の拡張子は中身ごと、それ以外はハッシュ（とサイズ・更新日時）だけを保存する
- `file_watch_mark_edit`(PostToolUse, matcher=Edit|Write): Edit/Writeしたファイルのハッシュを、どのセッションが書いたかと一緒に登録簿へ記録
- `file_watch_check`(UserPromptSubmit): ユーザー発言のたびにスナップショットと現在の中身を突き合わせ、変わっていれば差分をコンテキストへ注入。ハッシュだけを保存したファイルは、中身も差分も出さず変わったことだけを伝える（ハッシュを取り直すのはサイズか更新日時が変わったときだけ）。Claude Code自身の変更なら登録簿と照合して「自分の変更」「別セッションの変更」と注記する
- `hooks_setup`: 上の3本をビルド済みexeとして`~/.claude/hooks/`へ配置し、`~/.claude/settings.json`のhooks設定へ登録する（ダブルクリックでインストール、`-uninstall`で削除）。登録済みのエントリがあれば、それを書き換える。`-config` / `-config-add`（下記）を渡すと、`file_watch_track` と `file_watch_check` のコマンドに付ける

## 通知の言語

`file_watch_check` がコンテキストに差し込む通知は、英語（既定）か日本語。
`hooks_setup` を実行すると、どちらにするかを尋ねるダイアログが出る
（`hooks_setup.exe -lang ja` のように指定すれば尋ねない）。選んだ言語は、
settings.json に登録するコマンドの引数 `-lang` として残る。

## 差分を出す拡張子の指定

既定では、差分を出すのは組み込みの一覧（`internal/filewatch/common.go` の
`TextExtensions`）。JSONファイルで、これを置き換えたり足したりできる。

```json
{"extensions": [".txt", ".md", ".foo"]}
```

各要素は小文字にされ、先頭の `.` が無ければ補われる。空配列も有効。

- `-config <JSONのパス>`: JSONの一覧だけを使う（組み込みの一覧を置き換える）。空配列なら、全部ハッシュのみで追跡する
- `-config-add <JSONのパス>`: 組み込みの一覧にJSONの一覧を足したものを使う。空配列なら組み込みと同じ

どちらも `file_watch_track` と `file_watch_check` の引数。通知の言語はJSONには
入れず、従来どおり `-lang`。

ファイルを使えないとき（無い・読めない・JSONが壊れている・`extensions` が文字列の
配列でない・`-config` と `-config-add` が両方付いている）は、組み込みの一覧で動く。
`file_watch_track` は黙って続行し、`file_watch_check` は、どのファイルをなぜ読めない
かを示す1行を、他に通知が無い場合も含めて発言のたびにコンテキストへ加える
（言語は `-lang` に従う）。

`hooks_setup.exe -config <パス>` または `-config-add <パス>` を付けて実行すると、
パスを絶対パスにして、登録する `file_watch_track` と `file_watch_check` のコマンドに
付ける（空白や日本語を含むパスはクォートされる）。片方を指定すると、登録済みの
コマンドからもう片方は外れる。両方の同時指定はエラーで、何も変更しない。どちらも
付けずに再実行したときは、登録済みのコマンドにある指定をそのまま残す。
`-config ""`（または `-config-add ""`）を付けると両方とも外れる。JSONファイルは
`hooks_setup` では作らない。

## ビルド

Go 1.27以降が必要。`go/` で次を実行する（手元では `build.ps1` が同じことをして `bin/` に出す）。

```
go build -trimpath -o bin/file_watch_track.exe ./cmd/file_watch_track
go build -trimpath -o bin/file_watch_mark_edit.exe ./cmd/file_watch_mark_edit
go build -trimpath -o bin/file_watch_check.exe ./cmd/file_watch_check
go build -trimpath -o bin/hooks_setup.exe ./cmd/hooks_setup
```

`-trimpath` を付けるのは、ビルドした環境のファイルパスを exe に残さないため。

## 前提

Windows専用。stateの置き場所は`~/.claude/tmp/file_watch`固定で、追跡するファイル1件につき1つの小さなファイルに分けて置く。セッションごとに読んだファイルは`sessions/{session_id}/`、Claude Codeが書いたファイルは`claude_edits/`。同時に走るフック（並列のRead、複数のセッション）は別々のファイルに書くので、互いの書き込みを消すことがなく、ロックも使わない。以前の版のstate（`{session_id}.json`・`{session_id}.hash_only.json`・`claude_edits.json`）は読まない。更新する前に読んだファイルは、Claude Codeが次に読んだときから追跡し直す。
差分を出すのは、既定では`.txt`/`.md`/`.py`など主要なテキスト系拡張子のみ（`internal/filewatch/common.go`の`TextExtensions`。「差分を出す拡張子の指定」参照）。それ以外（画像・PDF・`.ipynb`など）は、変わったことだけを伝える。

ビルド済みのexe（`hooks_setup.exe` と、同じフォルダに置く3本の `file_watch_*.exe`）は
https://github.com/noileg/croco-editor の直下にある。
