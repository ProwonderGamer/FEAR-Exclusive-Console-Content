#!/bin/bash
# usage: mkfix2.sh <fixtures from cmd/mkfixtures> <output dir>  (extra fixtures for ui_test.mjs, mostly symlinks)
set -e
F=${1:?fixtures dir}; G=${2:?output dir}; mkdir -p "$G"; cd "$G"
PS3G="$F/ps3_disc_folder/PS3_GAME/USRDIR/game"
X360="$F/x360_folder"
mkexe(){ mkdir -p "$1"; printf 'MZ' > "$1/FEAR.exe"; printf 'x' > "$1/FEAR_8.Arch00"; }
# ---------------- PC folders
mkdir -p pc_empty
mkexe pc_noecho
mkexe pc_foreign; printf 'MZ not the one' > pc_foreign/dinput8.dll
mkexe pc_ini_only; printf '[Fixes]' > pc_ini_only/EchoPatch.ini
mkdir -p pc_upper; printf 'MZ' > pc_upper/FEAR.EXE; printf 'x' > pc_upper/fear_8.arch00; printf 'E\0c\0h\0o\0P\0a\0t\0c\0h\0' > pc_upper/DINPUT8.DLL
mkdir -p pc_nopatch; printf 'MZ' > pc_nopatch/FEAR.exe; printf 'x' > pc_nopatch/FEAR.Arch00; printf 'EchoPatch' > pc_nopatch/dinput8.dll
mkdir -p "pc_parent/FEAR Ultimate Shooter Edition"; cp pc_upper/* "pc_parent/FEAR Ultimate Shooter Edition/" 2>/dev/null || true
mkdir -p "pc_unicode/F.E.A.R. Ünïcode 日本語 & spaces  (x86)"; U="pc_unicode/F.E.A.R. Ünïcode 日本語 & spaces  (x86)"; printf MZ > "$U/FEAR.exe"; printf x > "$U/FEAR_8.Arch00"; printf EchoPatch > "$U/dinput8.dll"
L=pc_long; for i in $(seq 1 14); do L="$L/very_long_folder_name_segment_$i"; done; mkdir -p "$L"; printf MZ > "$L/FEAR.exe"; printf x > "$L/FEAR_8.Arch00"; printf EchoPatch > "$L/dinput8.dll"; echo "$G/$L" > long_path.txt
mkexe pc_ro; printf EchoPatch > pc_ro/dinput8.dll; chmod -R a-w pc_ro
echo hello > not_a_folder.txt
mkexe pc_late
# FEAR.exe is a directory (weird)
mkdir -p pc_exe_is_dir/FEAR.exe
# ---------------- second Steam layout (library elsewhere, custom installdir, unicode)
mkdir -p SteamB/steamapps "lib2 ünï/steamapps/common/FEAR Custom Dir"
cat > SteamB/steamapps/libraryfolders.vdf <<V
"libraryfolders"
{
	"0"
	{
		"path"		"$G/SteamB"
		"apps" { "1" "2" }
	}
	"1"
	{
		"path"		"$G/lib2 ünï"
		"apps" { "21090" "123" }
	}
}
V
cat > "lib2 ünï/steamapps/appmanifest_21090.acf" <<V
"AppState"
{
	"appid"		"21090"
	"installdir"		"FEAR Custom Dir"
}
V
D="lib2 ünï/steamapps/common/FEAR Custom Dir"; printf MZ > "$D/FEAR.exe"; printf x > "$D/FEAR_8.Arch00"; printf EchoPatch > "$D/dinput8.dll"
# ---------------- PS3 variants
lnk(){ ln -sfn "$1" "$2"; }
mkdir -p ps3_levels; lnk "$F/ps3_disc_folder/PS3_GAME" ps3_levels/PS3_GAME_only; lnk "$F/ps3_disc_folder/PS3_GAME/USRDIR" ps3_levels/USRDIR_only; lnk "$PS3G" ps3_levels/game_only
mkdir -p ps3_parent; lnk "$F/ps3_disc_folder" ps3_parent/MyDisc
# deep copy-with-symlinks helper
cpl(){ rm -rf "$2"; mkdir -p "$2"; (cd "$1" && find . -type d -exec mkdir -p "$2/{}" \; ; find . -type f -o -type l | while read f; do ln -s "$(readlink -f "$1/$f")" "$2/$f"; done); }
cpl "$F/ps3_disc_folder" "$G/ps3_wrongtitle"; rm "$G/ps3_wrongtitle/PS3_GAME/PARAM.SFO"; sed 's/BLUS30003/BLUS30004/' "$F/ps3_disc_folder/PS3_GAME/PARAM.SFO" > "$G/ps3_wrongtitle/PS3_GAME/PARAM.SFO"
cpl "$F/ps3_disc_folder" "$G/ps3_noparam"; rm "$G/ps3_noparam/PS3_GAME/PARAM.SFO"
cpl "$F/ps3_disc_folder" "$G/ps3_nouber"; rm "$G/ps3_nouber/PS3_GAME/USRDIR/game/ubercatalog.cat"
cpl "$F/ps3_disc_folder" "$G/ps3_smalluber"; rm "$G/ps3_smalluber/PS3_GAME/USRDIR/game/ubercatalog.cat"; truncate -s 1000000 "$G/ps3_smalluber/PS3_GAME/USRDIR/game/ubercatalog.cat"
cpl "$F/ps3_disc_folder" "$G/ps3_flipped"; rm "$G/ps3_flipped/PS3_GAME/USRDIR/game/database/loki.gamedb"; cp "$PS3G/database/loki.gamedb" "$G/ps3_flipped/PS3_GAME/USRDIR/game/database/loki.gamedb"; chmod u+w "$G/ps3_flipped/PS3_GAME/USRDIR/game/database/loki.gamedb"; printf '\x55' | dd of="$G/ps3_flipped/PS3_GAME/USRDIR/game/database/loki.gamedb" bs=1 seek=1000000 conv=notrunc 2>/dev/null
cpl "$F/ps3_disc_folder" "$G/ps3_missingfile"; rm "$G/ps3_missingfile/PS3_GAME/USRDIR/game/database_anim/anim.gamedb"
cpl "$F/ps3_disc_folder" "$G/ps3_emptyfile"; rm "$G/ps3_emptyfile/PS3_GAME/USRDIR/game/snd/sfx.cat"; : > "$G/ps3_emptyfile/PS3_GAME/USRDIR/game/snd/sfx.cat"
# encrypted-looking folder: use standins
cpl "$F/ps3_disc_folder" "$G/ps3_encfolder"; for f in $(cd "$G/ps3_encfolder" && find . -type l ! -name PARAM.SFO ! -name ICON0.PNG); do b=$(basename $f); rm "$G/ps3_encfolder/$f"; ln -s "$F/_standins/enc_$b" "$G/ps3_encfolder/$f"; done
# half-encrypted: only one file random
cpl "$F/ps3_disc_folder" "$G/ps3_enc_one"; rm "$G/ps3_enc_one/PS3_GAME/USRDIR/game/snd/english.cat"; ln -s "$F/_standins/enc_english.cat" "$G/ps3_enc_one/PS3_GAME/USRDIR/game/snd/english.cat"
# case differences (PS3 folder in upper case)
mkdir -p "ps3_upper"; cpl "$PS3G" "$G/ps3_upper/GAME_UP"
# truncated ISOs
head -c 300000000 "$F/ps3_decrypted.iso" > ps3_trunc_half.iso
head -c 100000 "$F/ps3_decrypted.iso" > ps3_trunc_tiny.iso
head -c 400000000 "$F/x360_xgd2.iso" > x360_trunc.iso
: > zero.iso; echo "just text" > text.iso
truncate -s 60G huge_sparse.iso
cp "$F/ps3_decrypted.iso" "ps3 ünï 日本語 & spaces.iso" 2>/dev/null || ln -s "$F/ps3_decrypted.iso" "ps3 ünï 日本語 & spaces.iso"
# ---------------- X360 variants
cpl "$X360" "$G/x360_flipped"; for f in Game/database/Loki.gamedb; do rm "$G/x360_flipped/$f"; cp "$X360/$f" "$G/x360_flipped/$f"; chmod u+w "$G/x360_flipped/$f"; printf '\x55' | dd of="$G/x360_flipped/$f" bs=1 seek=500000 conv=notrunc 2>/dev/null; done
cpl "$X360" "$G/x360_nouber"; rm "$G/x360_nouber/Game/uberCatalog.cat"
cpl "$X360" "$G/x360_badubersize"; rm "$G/x360_badubersize/Game/uberCatalog.cat"; truncate -s 428074095 "$G/x360_badubersize/Game/uberCatalog.cat"
cpl "$X360" "$G/x360_noxex"; rm "$G/x360_noxex/default.xex"
mkdir -p x360_gameonly; ln -sfn "$X360/Game" x360_gameonly/Game; lnk "$X360/Game" x360_gameonly_direct
mkdir -p x360_parent; lnk "$X360" x360_parent/MyX360
mkdir -p empty_folder
mkdir -p loop; ln -sfn "$G/loop" loop/self
echo done
