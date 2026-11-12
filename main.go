package main

import (
	"embed"
	"log"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	if isReaderWindow(os.Args[1:]) {
		window, err := newReaderWindow(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		runReaderWindow(window)
		return
	}
	app := NewApp()
	var appMenu *menu.Menu
	if runtime.GOOS == "darwin" {
		appMenu = menu.NewMenu()
		appMenu.Append(menu.AppMenu())
		appMenu.Append(menu.EditMenu())
	}
	err := wails.Run(&options.App{
		Title: "SpineGuard", Width: 1320, Height: 860, MinWidth: 980, MinHeight: 680,
		BackgroundColour: options.NewRGB(15, 20, 30),
		AssetServer:      &assetserver.Options{Assets: assets, Middleware: readerAssetMiddleware(app.reader.ServeHTTP, false)},
		OnStartup:        app.startup, OnDomReady: app.domReady, OnShutdown: app.shutdown,
		Menu: appMenu, Bind: []interface{}{app},
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true},
		Windows:     &windows.Options{Theme: windows.SystemDefault},
		Mac: &mac.Options{TitleBar: mac.TitleBarDefault(), About: &mac.AboutInfo{
			Title: "SpineGuard", Message: "Version " + version + "\nEPUB health checks, reading statistics, and repairs.",
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func runReaderWindow(window *ReaderWindow) {
	var readerMenu *menu.Menu
	if runtime.GOOS == "darwin" {
		readerMenu = menu.NewMenu()
		readerMenu.Append(menu.AppMenu())
		readerMenu.Append(menu.EditMenu())
	}
	err := wails.Run(&options.App{
		Title: window.title(), Width: 760, Height: 980, MinWidth: 560, MinHeight: 680,
		StartHidden:      true,
		BackgroundColour: options.NewRGB(15, 20, 30),
		AssetServer:      &assetserver.Options{Assets: assets, Middleware: readerAssetMiddleware(window.serveHTTP, true)},
		OnStartup:        window.startup, OnDomReady: window.domReady, OnShutdown: window.shutdown,
		OnBeforeClose: window.beforeClose, Bind: []interface{}{window}, Menu: readerMenu,
		DragAndDrop: &options.DragAndDrop{DisableWebViewDrop: true},
		Windows:     &windows.Options{Theme: windows.SystemDefault, WebviewUserDataPath: window.webviewDataPath()},
		Mac:         &mac.Options{TitleBar: mac.TitleBarDefault()},
	})
	if err != nil {
		log.Fatal(err)
	}
}
