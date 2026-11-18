package main

import (
	"embed"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	_ "github.com/egoist/mygo/locales/ja"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	_ "github.com/egoist/mygo/locales/zh-Hant"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/atomicfile"
	"syncmaid/internal/winutil"
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

var languageChoices = []struct{ tag, name string }{
	{"en", "English"}, {"ja", "日本語"}, {"zh-Hans", "简体中文"}, {"zh-Hant", "繁體中文"},
}

func supportedLanguage(tag string) bool {
	for _, choice := range languageChoices {
		if choice.tag == tag {
			return true
		}
	}
	return false
}

// MyGo owns the one language file. Check before its non-reporting SetLocale
// write, then verify persistence. A malformed existing choice is never reset.
func checkLanguageFile(data string) (string, error) {
	path := filepath.Join(data, "locale")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !atomicfile.IsRegularFile(info) {
		return "", fmt.Errorf("language file is not an ordinary file: %s", winutil.PathLabel(path))
	}
	if info.Size() > 128 {
		return "", errors.New("language file exceeds 128 bytes; preserve and repair it before changing language")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 129))
	err = errors.Join(readErr, f.Close())
	if err != nil {
		return "", err
	}
	tag := strings.TrimSpace(string(b))
	if len(b) > 128 || !supportedLanguage(tag) {
		return "", fmt.Errorf("unsupported or damaged language choice in %s", winutil.PathLabel(path))
	}
	return tag, nil
}

func prepareLanguageChange(data, tag string) error {
	if tag != "" && !supportedLanguage(tag) {
		return errors.New("unsupported language")
	}
	if _, err := checkLanguageFile(data); err != nil {
		return err
	}
	// Do not overwrite an unknown leftover file using the framework's fixed temp.
	_, err := os.Lstat(filepath.Join(data, "locale.tmp"))
	if err == nil {
		return errors.New("locale.tmp already exists; preserve and inspect it before changing language")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func verifyLanguageChange(data, tag string) error {
	actual, err := checkLanguageFile(data)
	if err != nil {
		return err
	}
	if actual != tag {
		return errors.New("language was not saved; check directory permissions or MYGO_LOCALE development override")
	}
	return nil
}

func (a *application) changeLanguage(tag string) {
	if a.languageBusy {
		return
	}
	a.languageBusy = true
	data := a.installation.Data
	go func() {
		err := prepareLanguageChange(data, tag)
		a.window.Update(func() {
			if err != nil {
				a.finishLanguageChange(err)
				return
			}
			// MyGo's small locale write is synchronous on the native thread;
			// checking files and verifying its result stay off that thread.
			mygo.App.SetLocale(tag)
			go func() {
				err := verifyLanguageChange(data, tag)
				a.window.Update(func() {
					if err == nil {
						a.languagePreference = tag
					}
					a.finishLanguageChange(err)
				})
			}()
		})
	}()
}
func (a *application) finishLanguageChange(err error) {
	a.languageBusy = false
	a.languageError = err
	a.finishAsyncStep(false)
}
