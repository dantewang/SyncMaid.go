package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

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
	if !info.Mode().IsRegular() || languageReparse(info) {
		return "", fmt.Errorf("language file is not an ordinary file: %s", pathLabel(path))
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
		return "", fmt.Errorf("unsupported or damaged language choice in %s", pathLabel(path))
	}
	return tag, nil
}

func prepareLanguageChange(data, tag string) error {
	if !supportedLanguage(tag) {
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
	data := a.installation.data
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
				a.window.Update(func() { a.finishLanguageChange(err) })
			}()
		})
	}()
}
func (a *application) finishLanguageChange(err error) {
	a.languageBusy = false
	a.languageError = err
	if a.manual.quitPending {
		a.requestQuit()
	}
}
