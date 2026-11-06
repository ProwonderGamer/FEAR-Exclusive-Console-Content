// Command pecheck validates the built installer: a 64-bit Windows GUI
// executable that imports only system DLLs and carries its manifest.
//
//	go run ./cmd/pecheck dist/FEAR-Exclusive-Console-Content-Installer.exe
package main

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: pecheck <exe>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := pe.NewFile(bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "not a valid PE file:", err)
		os.Exit(1)
	}
	fail := 0
	check := func(ok bool, what string) {
		mark := "PASS"
		if !ok {
			mark, fail = "FAIL", fail+1
		}
		fmt.Printf("%s  %s\n", mark, what)
	}

	oh, _ := f.OptionalHeader.(*pe.OptionalHeader64)
	check(f.Machine == pe.IMAGE_FILE_MACHINE_AMD64, fmt.Sprintf("machine is x64 (0x%X)", f.Machine))
	check(oh != nil && oh.Magic == 0x20b, "optional header is PE32+")
	check(f.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE != 0 && f.Characteristics&pe.IMAGE_FILE_DLL == 0, "is an executable, not a DLL")
	if oh != nil {
		check(oh.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI, fmt.Sprintf("subsystem is GUI (%d), no console window", oh.Subsystem))
		fmt.Printf("INFO  OS/subsystem version %d.%d, entry point 0x%X\n", oh.MajorSubsystemVersion, oh.MinorSubsystemVersion, oh.AddressOfEntryPoint)
	}

	libs, err := importedDLLs(f, raw)
	check(err == nil && len(libs) > 0, "import table readable")
	sort.Strings(libs)
	fmt.Println("INFO  imported DLLs:", strings.Join(libs, ", "))
	system := map[string]bool{"kernel32.dll": true, "user32.dll": true, "ole32.dll": true, "shell32.dll": true, "ntdll.dll": true, "advapi32.dll": true, "oleaut32.dll": true, "gdi32.dll": true, "shcore.dll": true, "winmm.dll": true, "ws2_32.dll": true}
	bad := []string{}
	for _, l := range libs {
		if !system[strings.ToLower(l)] {
			bad = append(bad, l)
		}
	}
	check(len(bad) == 0, fmt.Sprintf("imports only Windows system DLLs (unexpected: %v)", bad))

	check(bytes.Contains(raw, []byte("requestedExecutionLevel")) && bytes.Contains(raw, []byte("asInvoker")), "manifest embedded: asInvoker, no elevation")
	check(bytes.Contains(raw, []byte("PerMonitorV2")), "manifest embedded: per-monitor DPI awareness")
	check(bytes.Contains(raw, []byte("WebView2Loader")), "WebView2 loader is embedded (single self-contained file)")
	check(bytes.Contains(raw, []byte("be_browse")) && bytes.Contains(raw, []byte("be_install")) && bytes.Contains(raw, []byte("Choose your console content")), "HTML UI is embedded")
	// ui/THIRD_PARTY_LICENSES.txt: its heading plus a phrase from the BSD (Go), ISC (go-winloader),
	// LGPL-2.1 (FFmpeg-derived decoders), FreeType and Pillow license texts it joins.
	check(bytes.Contains(raw, []byte("F.E.A.R. Exclusive Console Content - third-party licenses")) &&
		bytes.Contains(raw, []byte("Redistribution and use in source and binary forms")) &&
		bytes.Contains(raw, []byte("ISC License")) &&
		bytes.Contains(raw, []byte("GNU LESSER GENERAL PUBLIC LICENSE")) &&
		bytes.Contains(raw, []byte("The FreeType Project LICENSE")) &&
		bytes.Contains(raw, []byte("the name of Secret Labs AB or the author")), "third-party license texts are embedded")
	check(bytes.Contains(raw, []byte("c4674acf95f0800793a4a6d61132adf5dfa694c218e482d86093494c4e84100a")), "embedded WebView2 loader is pinned by SHA-256")
	check(bytes.Contains(raw, []byte("SetDefaultDllDirectories")) && bytes.Contains(raw, []byte("LoadLibraryExW")), "DLL search order is restricted (SetDefaultDllDirectories / LoadLibraryEx)")
	// Build-machine paths: the usual home/temp roots plus this very working directory and home folder.
	needles := []string{"/home/", "/root/", "/tmp/", "/mnt/", "/Users/", `C:\Users\`}
	if wd, err := os.Getwd(); err == nil && len(wd) > 3 {
		needles = append(needles, wd)
	}
	if home, err := os.UserHomeDir(); err == nil && len(home) > 3 {
		needles = append(needles, home)
	}
	leaks := []string{}
	for _, s := range needles {
		if bytes.Contains(raw, []byte(s)) {
			leaks = append(leaks, s)
		}
	}
	check(len(leaks) == 0, fmt.Sprintf("no build-machine paths in the file (found: %v)", leaks))
	check(rsrcHasIcon(f, raw), "application icon (RT_GROUP_ICON) is present in the resources")

	st, _ := os.Stat(os.Args[1])
	fmt.Printf("INFO  size %d bytes, sha256 %x\n", st.Size(), sha256.Sum256(raw))
	if fail > 0 {
		fmt.Printf("%d check(s) failed\n", fail)
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}

// importedDLLs reads the import directory directly. (debug/pe's helper returns
// nothing for Go's linker output, which keeps its imports in a bare .idata.)
func importedDLLs(f *pe.File, raw []byte) ([]string, error) {
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return nil, fmt.Errorf("not PE32+")
	}
	dir := oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_IMPORT]
	rva2off := func(rva uint32) (int, bool) {
		for _, s := range f.Sections {
			if rva >= s.VirtualAddress && rva < s.VirtualAddress+max(s.VirtualSize, s.Size) {
				return int(rva-s.VirtualAddress) + int(s.Offset), true
			}
		}
		return 0, false
	}
	off, ok := rva2off(dir.VirtualAddress)
	if !ok || dir.Size == 0 {
		return nil, fmt.Errorf("no import directory")
	}
	var libs []string
	for ; off+20 <= len(raw); off += 20 {
		nameRVA := binary.LittleEndian.Uint32(raw[off+12:])
		if nameRVA == 0 {
			break
		}
		no, ok := rva2off(nameRVA)
		if !ok {
			return nil, fmt.Errorf("bad import name RVA")
		}
		end := bytes.IndexByte(raw[no:], 0)
		if end < 0 {
			return nil, fmt.Errorf("import name not terminated")
		}
		libs = append(libs, strings.ToLower(string(raw[no:no+end])))
	}
	return libs, nil
}

// rsrcHasIcon walks the resource directory for a RT_GROUP_ICON (14) entry.
func rsrcHasIcon(f *pe.File, raw []byte) bool {
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return false
	}
	dir := oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
	for _, s := range f.Sections {
		if dir.VirtualAddress >= s.VirtualAddress && dir.VirtualAddress < s.VirtualAddress+max(s.VirtualSize, s.Size) {
			base := int(s.Offset) + int(dir.VirtualAddress-s.VirtualAddress)
			if base+16 > len(raw) {
				return false
			}
			named := int(binary.LittleEndian.Uint16(raw[base+12:]))
			ids := int(binary.LittleEndian.Uint16(raw[base+14:]))
			for i := 0; i < named+ids; i++ {
				e := base + 16 + 8*i
				if e+8 > len(raw) {
					return false
				}
				if binary.LittleEndian.Uint32(raw[e:]) == 14 {
					return true
				}
			}
		}
	}
	return false
}
