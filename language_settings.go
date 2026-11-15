package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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

func (a *application) settingsView(c *ui.Context) {
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(12).Padding(20).Children(func() {
			ui.Text(c, a.tr("设置")).FontSize(22).Bold()
			a.commonSettingsView(c)
			ui.Text(c, a.tr("界面语言"))
			for _, choice := range languageChoices {
				tag := choice.tag
				ui.Button(c, choice.name).Selected(a.locale == tag).Disabled(a.languageBusy || a.manual.phase == manualLoading).OnClick(func() { a.changeLanguage(tag) })
			}
			ui.Text(c, a.tr("语言立即生效，自动保存在 Data/locale；任务名称、路径和表达式保持原样。"))
			if a.languageBusy {
				ui.Text(c, a.tr("正在保存语言…"))
			}
			if a.languageError != nil {
				ui.Text(c, a.tr("语言设置未能保存或读取；请保留并检查 Data/locale 后重试。当前界面可能只在本次生效。"))
				ui.Text(c, a.languageError.Error()).FontSize(12)
			}
			ui.Text(c, a.tr("取得 Data 所有权前的启动错误仍使用简体中文。"))
		})
	})
}
