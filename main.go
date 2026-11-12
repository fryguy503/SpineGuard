package main

import (
	"embed"
	"log"
	"net/http"
	"runtime"
	"strings"

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
		AssetServer: &assetserver.Options{Assets: assets, Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/__reader/") {
					app.reader.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r)
			})
		}},
		OnStartup: app.startup, OnDomReady: app.domReady, OnShutdown: app.shutdown,
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
