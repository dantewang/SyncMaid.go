package main

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/egoist/mygo/i18n"
	_ "github.com/egoist/mygo/locales/ja"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	_ "github.com/egoist/mygo/locales/zh-Hant"
)

//go:embed locales/*.json
var languageFiles embed.FS

var messages = func() *i18n.Catalog {
	files, err := fs.Sub(languageFiles, "locales")
	if err != nil {
		panic(err)
	}
	c, err := i18n.NewCatalog(files, "en")
	if err != nil {
		panic(err)
	}
	i18n.Register(c)
	return c
}()

// Presentation messages keep arguments separate from translation keys. User
// names, paths and diagnostic text never become keys during a language change.
type localizedMessage struct {
	key  string
	args []any
}

func messagef(key string, args ...any) localizedMessage { return localizedMessage{key, args} }
func (m localizedMessage) render(locale string) string {
	args := append([]any(nil), m.args...)
	for i, arg := range args {
		if nested, ok := arg.(localizedMessage); ok {
			args[i] = nested.render(locale)
		}
	}
	format := messages.Format(locale, m.key, nil)
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// These accessors belong to native view/event callbacks, never worker code.
func (a *application) tr(key string, args ...any) string {
	return messagef(key, args...).render(a.locale)
}
func (a *application) render(m localizedMessage) string { return m.render(a.locale) }
