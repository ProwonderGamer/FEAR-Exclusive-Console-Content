package install

import _ "embed"

// HookTemplate is this mod's dinput8_hook.dll (EchoPatch loads it) with every value that comes from the games
// set to zero: the numbers of its Xbox 360 HUD layout table, a few short game texts, and the bytes it expects
// to find in the game's program files before it patches them. They are not shipped;
// the installer fills them in from the player's own copies at install time (internal/hookfill, app.BuildHook).
//
//go:embed payload/dinput8_hook_template.dll
var HookTemplate []byte

// TemplateSHA256 is the SHA-256 of HookTemplate; TestPayload keeps the two in step.
const TemplateSHA256 = "4dc4c0cf47f6f751f08a2ab831acd358ea49975d1f7a0b6ec15ccaab6dbc4be7"

// HookSHA256 is the SHA-256 of the hook once its values are filled in: the build that was tested in game.
// The installer writes no other hook.
const HookSHA256 = "a2db4957f170718a309fbeab5ae834551b11dbd6f41a694b75e1a670ac729f9b"
