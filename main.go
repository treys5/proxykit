package main

// Embed Windows version info + icon into the exe.
// Run: go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
// Then: go generate
//go:generate goversioninfo -icon=icon.ico -manifest=ProxyKit.exe.manifest

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func main() {
	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}

	// Data directory — always use %LOCALAPPDATA%\ProxyKit so every copy of the
	// exe (desktop, installed, dev) shares the same auth token, saved lists, etc.
	if d := os.Getenv("APP_DATA_DIR"); d != "" {
		DataDir = d
	} else {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = os.Getenv("APPDATA") // fallback for very old Windows
		}
		if local != "" {
			DataDir = filepath.Join(local, "ProxyKit")
		} else {
			DataDir = "." // last resort (dev machines without LOCALAPPDATA)
		}
	}
	os.MkdirAll(DataDir, 0755)

	// Ensure results directory exists
	os.MkdirAll(filepath.Join(DataDir, "results"), 0755)

	// Load persisted providers into memory
	globalMu.Lock()
	globalProviders = loadProviders()
	globalMu.Unlock()

	// Load persisted cloud auth token
	loadCloudAuth()

	// Load schedules and start scheduler
	loadSchedules()
	startScheduler()

	// Start drop scheduler
	startDropScheduler()

	// Start background PX protection monitor
	startPXMonitor()

	// Start HTTP server in background
	addr := fmt.Sprintf(":%d", port)
	router := NewRouter()
	server := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// Brief pause so the HTTP server is ready before the window navigates
	time.Sleep(150 * time.Millisecond)

	url := fmt.Sprintf("http://localhost:%d", port)

	// Open a native WebView2 window (uses Edge, ships with Windows 10/11)
	w := webview.New(false)
	if w == nil {
		// WebView2 runtime not present — fall back to browser tab
		fmt.Fprintln(os.Stderr, "WebView2 not available, opening in browser")
		openBrowser(url)
		// Keep the server alive until Ctrl-C
		select {}
	}
	defer w.Destroy()

	w.SetTitle(fmt.Sprintf("ProxyKit v%s", AppVersion))
	w.SetSize(1400, 900, webview.HintNone)

	// Tint the native Win32 title bar to match the app's dark theme.
	// DWMWA_CAPTION_COLOR (35) and DWMWA_TEXT_COLOR (36) require Windows 11 build 22000+.
	// On older Windows the calls are no-ops (DWM returns an error we silently ignore).
	applyWindowTheme(w.Window())
	setWindowIcon(w.Window())

	w.Navigate(url)
	w.Run() // blocks until the window is closed — app exits naturally
}

// setWindowIcon loads the app's icon (resource ID 1) from the exe's own resource
// section and sets it on the WebView2 HWND.  This makes the title bar and taskbar
// button show the ProxyKit gradient-hex logo instead of the default Windows icon.
// No-ops silently on failure (e.g. no embedded icon resource).
func setWindowIcon(hwndPtr unsafe.Pointer) {
	hwnd := windows.HWND(uintptr(hwndPtr))
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	loadImage := user32.NewProc("LoadImageW")
	sendMessage := user32.NewProc("SendMessageW")
	getModuleHandle := kernel32.NewProc("GetModuleHandleW")

	const (
		IMAGE_ICON = 1
		LR_SHARED  = 0x8000
		WM_SETICON = 0x0080
		ICON_SMALL = 0
		ICON_BIG   = 1
	)

	// GetModuleHandleW(nil) returns the HINSTANCE of the running exe
	hMod, _, _ := getModuleHandle.Call(0)

	// Resource ID 1 is the first ICON resource — set by goversioninfo via versioninfo.json
	iconBig, _, _ := loadImage.Call(hMod, 1, IMAGE_ICON, 32, 32, LR_SHARED)
	iconSmall, _, _ := loadImage.Call(hMod, 1, IMAGE_ICON, 16, 16, LR_SHARED)

	if iconBig != 0 {
		sendMessage.Call(uintptr(hwnd), WM_SETICON, ICON_BIG, iconBig)
	}
	if iconSmall != 0 {
		sendMessage.Call(uintptr(hwnd), WM_SETICON, ICON_SMALL, iconSmall)
	}
}

// applyWindowTheme colours the Win32 titlebar to match the app palette.
//
//   --bg      #0d1117  → COLORREF 0x0017110d  (caption background)
//   --text    #dce8f5  → COLORREF 0x00f5e8dc  (caption text)
//   --border  #242e42  → COLORREF 0x00422e24  (window border)
//
// Works on Windows 11 22000+; silently ignored on older builds.
func applyWindowTheme(hwndPtr unsafe.Pointer) {
	hwnd := windows.HWND(uintptr(hwndPtr))
	dwmapi := windows.NewLazySystemDLL("dwmapi.dll")
	setAttr := dwmapi.NewProc("DwmSetWindowAttribute")

	const (
		DWMWA_USE_IMMERSIVE_DARK_MODE = 20 // enables dark mode chrome (Win10 21H1+)
		DWMWA_BORDER_COLOR            = 34 // Win11 22000+
		DWMWA_CAPTION_COLOR           = 35 // Win11 22000+
		DWMWA_TEXT_COLOR              = 36 // Win11 22000+
	)

	darkMode := uint32(1)
	captionColor := uint32(0x0017110d) // #0d1117 as COLORREF (0x00BBGGRR)
	textColor := uint32(0x00f5e8dc)    // #dce8f5
	borderColor := uint32(0x00422e24)  // #242e42

	setAttr.Call(uintptr(hwnd), DWMWA_USE_IMMERSIVE_DARK_MODE, uintptr(unsafe.Pointer(&darkMode)), unsafe.Sizeof(darkMode))
	setAttr.Call(uintptr(hwnd), DWMWA_CAPTION_COLOR, uintptr(unsafe.Pointer(&captionColor)), unsafe.Sizeof(captionColor))
	setAttr.Call(uintptr(hwnd), DWMWA_TEXT_COLOR, uintptr(unsafe.Pointer(&textColor)), unsafe.Sizeof(textColor))
	setAttr.Call(uintptr(hwnd), DWMWA_BORDER_COLOR, uintptr(unsafe.Pointer(&borderColor)), unsafe.Sizeof(borderColor))
}

// openBrowser is the fallback when WebView2 is unavailable.
func openBrowser(url string) {
	exec.Command("cmd", "/c", "start", url).Start()
}
