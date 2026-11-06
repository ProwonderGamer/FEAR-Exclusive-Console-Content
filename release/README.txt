F.E.A.R. Exclusive Console Content
==================================

This mod brings the content that only the console versions of F.E.A.R. had to the PC version:

- from the PlayStation 3 version: the SFOD-D bonus mission and the Watson SAS-12 shotgun;
- from the Xbox 360 version: the Holiday bonus mission, the SM15 machine pistol, the Instant
  Action maps and the Xbox 360 heads-up display.

The download contains no game files. The installer builds everything on your own computer
from your own copies of the game.


What you need
-------------

- F.E.A.R. for PC (Steam or GOG).
- EchoPatch by Wemino, installed in the game folder (https://github.com/Wemino/EchoPatch).
  The installer explains how to get it.
- Your own PlayStation 3 copy of F.E.A.R. as a decrypted disc folder, and your own Xbox 360
  copy as an extracted disc folder. Both are needed.
- Windows 10 or 11 with the Microsoft Edge WebView2 Runtime. It is already part of Windows 11
  and of most Windows 10 installs; the installer tells you if it is missing.


Installing
----------

1. Close F.E.A.R.
2. Run FEAR-Exclusive-Console-Content-Installer.exe.
3. Follow the steps: it finds your PC game, then asks for the two console copies and checks
   them. Install becomes available once every check has passed.

Start a new game after installing or updating the mod. Save files made before that may be
missing content or behave unexpectedly.


Playing
-------

- Bonus missions: Main Menu, then Bonus mission.
- Instant Action: Main Menu, then Instant action.
- Xbox 360 heads-up display: on by default. Switch between the Xbox 360 and PC layouts in
  Options, Game, HUD layout.
- Console weapons: press T to open the console, type the command and press Enter.
    Watson:  gimmegun zSFODD_AutoShotgun
    SM15:    gimmegun zSFODD_Machine Pistol
  The command guns gives every weapon.


Uninstalling
------------

Run the installer again and choose Uninstall. It removes the mod and puts back the game
files it changed.


If something goes wrong
-----------------------

The installer writes a log next to itself (FEAR-Exclusive-Console-Content-Installer.log),
with your account name hidden in paths. When an install or uninstall stops, the message on
screen says what to do, and the log shows what happened.


Files in this download
----------------------

FEAR-Exclusive-Console-Content-Installer.exe
                                    the installer
README.txt                          this file
Licenses\LICENSE.txt                the mod's license
Licenses\Go-LICENSE.txt             Go, the language the installer is written in
Licenses\Go-PATENTS.txt             Go's patent grant
Licenses\go-webview2-LICENSE.txt    the installer's window (go-webview2)
Licenses\go-ole-LICENSE.txt         one part of go-webview2 (from go-ole)
Licenses\go-winloader-LICENSE.txt   loads the window component (go-winloader)
Licenses\WebView2-SDK-LICENSE.txt   Microsoft's WebView2 loader, inside the installer
Licenses\FFmpeg-LICENSE.txt         the sound decoders, translated to Go from FFmpeg (LGPL 2.1)
Licenses\FreeType-LICENSE.txt       the text drawing, ported from FreeType
Licenses\Pillow-LICENSE.txt         the picture resizing, ported from Pillow


License
-------

Free to use, never for sale, and not to be re-uploaded as is. The full terms are in
Licenses\LICENSE.txt. Parts of the installer come from other authors (the files listed
above); those authors are not affiliated with this mod and do not endorse it.


Source code
-----------

The source code of the installer is published at:
https://github.com/ProwonderGamer/FEAR-Exclusive-Console-Content
It includes the sound decoders translated from FFmpeg, which the GNU Lesser General Public
License requires to be offered with the program, and everything needed to rebuild the
installer. The game add-on it installs (dinput8_hook.dll) is included there as a program,
with every value that comes from the game left blank; its own source code is not published.


This is an unofficial fan-made mod, not affiliated with or endorsed by Warner Bros., Sony
Interactive Entertainment or Microsoft. F.E.A.R. and Monolith are trademarks of Warner Bros.
Entertainment Inc. PlayStation is a trademark of Sony Interactive Entertainment Inc. Xbox 360
is a trademark of Microsoft Corporation.
