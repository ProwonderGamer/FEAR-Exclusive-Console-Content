# Third-party license files

Each `*.txt` file here is shipped in the release ZIP (`Licenses\` folder) and published with the source.
Layout: a short intro written by this project (ASCII, CRLF), a blank line, a line of 72 `-`, a blank line,
then the upstream text byte for byte (its own line endings and characters; never re-wrapped or re-spelled).
`ui/THIRD_PARTY_LICENSES.txt` (shown in the installer) is these files joined in this order;
`ui/licenses_test.go` checks both the upstream hashes below and that join.

| File | Upstream text taken from | SHA-256 of the upstream text | File size | SHA-256 of the file |
|---|---|---|---|---|
| `Go-LICENSE.txt` | Go 1.24.7 `LICENSE` (official go1.24.7.windows-amd64.zip); identical to golang.org/x/sys v0.33.0 `LICENSE` (commit 3d9a6b80792a3911da1fa665c959a5ede3abf476) | `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad` | 1903 | `eb3fffaa5ea95e9344abaf0315286f2bd315a4d7de4ef977fcec3492462e7ab7` |
| `Go-PATENTS.txt` | Go 1.24.7 `PATENTS`; identical to golang.org/x/sys v0.33.0 `PATENTS` | `96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc` | 1643 | `b648af178e02a749c08110f687b57369c84982345b76f6135aa16c84c0465073` |
| `go-webview2-LICENSE.txt` | github.com/jchv/go-webview2 `LICENSE` at commit 56598839c808a2340edee99204db479f410e9bf4 | `3f10f3374cf30076196eb239667ce6063c64e4275afd2b397f6343f69676bb79` | 1707 | `b083df42c69ff834d679bc3873a30aee15df6d3b0879f0e76acf25bfb55de2e3` |
| `go-ole-LICENSE.txt` | the license comment in github.com/jchv/go-webview2 `pkg/edge/guid.go` at commit 56598839c808, which that file adapts from github.com/go-ole/go-ole (unmodified by our patches) | `d883dbca28769ff23dd4368a8edb7ad9f9f23a8f86adde2875c6e41a828aac62` | 1493 | `5672875bdc50fb6af9dbb936ef475f00564cdb755c74956b9bf39eba25a5649f` |
| `go-winloader-LICENSE.txt` | github.com/jchv/go-winloader `LICENSE.md` at commit c1995be93bd1362cba4d3d0ba6bfe0efdd0bcaf8 | `5dd7f3cdb5d4270495fa5475adf81791fc3032a6ccae74b38f774eaa60831b21` | 1233 | `599e74113686e185b25493b1988c34d951bc563459a8875e28398022066d1960` |
| `WebView2-SDK-LICENSE.txt` | Microsoft WebView2 SDK `LICENSE.txt` as vendored in go-webview2 `webviewloader/sdk/` at commit 56598839c808 | `0af8f1b807512aae39c2ac1aa4d0cae65cabecb6fd554b8439a5162a0d6eca55` | 1948 | `4e2991e68545b80709213b48e2d98c5e50fd6885939a12ecb21faea54a504e5c` |
| `FFmpeg-LICENSE.txt` | FFmpeg `COPYING.LGPLv2.1` at tag n7.1 (commit b08d7969c550a804a59511c7b83f2dd8cc0499b8), https://raw.githubusercontent.com/FFmpeg/FFmpeg/n7.1/COPYING.LGPLv2.1, downloaded 2026-11-06 | `b634ab5640e258563c536e658cad87080553df6f34f62269a21d554844e58bfe` | 29350 | `486d384ff378db48bcf0e76c335f40ddc06f5b9bdaec0f825cc3831345723d88` |
| `FreeType-LICENSE.txt` | FreeType `docs/FTL.TXT` at tag VER-2-14-1 (commit 526ec5c47b9ebccc4754c85ac0c0cdf7c85a5e9b), https://gitlab.freedesktop.org/freetype/freetype/-/raw/VER-2-14-1/docs/FTL.TXT, downloaded 2026-11-06 | `5a5ee54c5001bbad1cdc1a57cc3dd4c42199b2da09d39c7ee41fab002d02967f` | 7385 | `abb90f34dbf1cff98bfbc227e0521a3e748526fab64340d986c69722393d7af6` |
| `Pillow-LICENSE.txt` | Pillow `LICENSE` at tag 12.1.1, https://raw.githubusercontent.com/python-pillow/Pillow/12.1.1/LICENSE (the file in the repository, LF line endings, UTF-8); the bytes were taken from the same text in the installed Pillow 12.1.1 wheel's `LICENSE` and checked against that URL | `17f240ae101143707e5e7303a5d800450d9ccc7475b463cedb555cefdb3c6ece` | 2076 | `ad8004ad3cdd56574a6b115fc1a508cf68927a8e83110e42f5b26702c0acea25` |
