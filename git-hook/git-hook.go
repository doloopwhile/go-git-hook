package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/codegangsta/cli"
)

var timings = []string{
	`applypatch-msg`,
	`pre-applypatch`,
	`post-applypatch`,
	`pre-commit`,
	`prepare-commit-msg`,
	`commit-msg`,
	`post-commit`,
	`pre-rebase`,
	`post-checkout`,
	`post-merge`,
	`pre-receive`,
	`update`,
	`post-update`,
	`pre-auto-gc`,
	`post-rewrite`,
}

const rootHookFormat = `#!/bin/sh
git hook test %s "$@"
`

var (
	version  string
	compiled time.Time
	author   string
	email    string
)

type UrlHook struct {
	url *url.URL
}

func ParseUrlHook(s string) (Hook, error) {
	if !(strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) {
		return nil, fmt.Errorf("'$%s' is not url of http", s)
	}
	url, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	return &UrlHook{url}, err
}

func (h *UrlHook) String() string {
	return h.url.String()
}

func (h *UrlHook) Name() string {
	return filepath.Base(h.url.Path)
}

func (h *UrlHook) Install(path string) error {
	resp, err := http.Get(h.url.String())
	if err != nil {
		return err
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, b, 0755)
}

type FileHook struct {
	path string
}

func ParseFileHook(s string) (Hook, error) {
	return &FileHook{s}, nil
}

func (h *FileHook) String() string {
	return h.path
}

func (h *FileHook) Name() string {
	return filepath.Base(h.path)
}

func (h *FileHook) Install(path string) error {
	return os.Symlink(h.path, path)
}

func gitDirPath() (string, error) {
	output, err := exec.Command("git", "rev-parse", "--git-dir").Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(output))
	return filepath.Abs(path)
}

func directoryExists(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return st.IsDir(), nil
}

func gitRebaseInProgress() (bool, error) {
	d, err := gitDirPath()
	if err != nil {
		return false, err
	}
	mergeExists, err := directoryExists(filepath.Join(d, "rebase-merge"))
	if err != nil {
		return false, err
	}
	rebaseExists, err := directoryExists(filepath.Join(d, "rebase-apply"))
	if err != nil {
		return false, err
	}
	return (mergeExists || rebaseExists), nil
}

type Hook interface {
	String() string
	Name() string
	Install(path string) error
}

func ParseHookString(s string) (Hook, error) {
	parsers := []func(s string) (Hook, error){
		ParseUrlHook,
		ParseFileHook,
	}

	var err error
	for _, parse := range parsers {
		h, err := parse(s)
		if err != nil {
			continue
		}
		return h, nil
	}
	return nil, err
}

func createRootHook() error {
	for _, t := range timings {
		script := fmt.Sprintf(rootHookFormat, t)

		d, err := gitDirPath()
		if err != nil {
			return err
		}

		err = os.MkdirAll(filepath.Join(d, "hooks"), 0755)
		if err != nil {
			return err
		}

		files := []struct {
			path    string
			content string
			perm    os.FileMode
		}{
			{filepath.Join(d, "hooks", t), script, 0755},
			{filepath.Join(d, "hooks", t+".hooks"), "", 0644},
		}

		for _, file := range files {
			fp, err := os.OpenFile(file.path, os.O_WRONLY|os.O_CREATE, file.perm)
			if err != nil {
				return err
			}
			defer fp.Close()
			fp.WriteString(file.content)
		}
	}
	return nil
}

func allHooks(timing string) ([]Hook, error) {
	d, err := gitDirPath()
	if err != nil {
		return nil, err
	}

	fp, err := os.Open(filepath.Join(d, "hooks", timing+".hooks"))
	if err != nil {
		return nil, err
	}
	defer fp.Close()

	sc := bufio.NewScanner(fp)
	lines := []string{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if len(line) > 0 {
			lines = append(lines, sc.Text())
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	hooks := []Hook{}
	for _, line := range lines {
		h, err := ParseHookString(line)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, h)
	}
	return hooks, nil
}

func updateHooks(timing string) error {
	hooks, err := allHooks(timing)
	if err != nil {
		return err
	}

	d, err := gitDirPath()
	if err != nil {
		return err
	}

	installedDir := filepath.Join(d, "hooks", timing+".installed")
	err = os.MkdirAll(installedDir, 0755)
	if err != nil {
		return err
	}

	matches, err := filepath.Glob(installedDir + "/*")
	if err != nil {
		return err
	}
	for _, f := range matches {
		if err := os.Remove(f); err != nil {
			return err
		}
	}

	for i, h := range hooks {
		err := updateHookScript(timing, i, h)
		if err != nil {
			return err
		}
	}
	return nil
}

func updateHookScript(timing string, i int, h Hook) error {
	d, err := gitDirPath()
	if err != nil {
		return err
	}

	d = filepath.Join(d, "hooks", timing+".installed")
	err = os.MkdirAll(d, 0755)
	if err != nil {
		return err
	}

	path := filepath.Join(d, fmt.Sprintf("%d-%s", i, h.Name()))

	fmt.Printf("installing %s as %s\n", h.Name(), path)
	return h.Install(path)
}

func installHook(timing, s string) error {
	h, err := ParseHookString(s)
	if err != nil {
		return err
	}

	hooks, err := allHooks(timing)

	err = updateHookScript(timing, len(hooks), h)
	if err != nil {
		return err
	}

	d, err := gitDirPath()
	if err != nil {
		return err
	}
	fp, err := os.OpenFile(
		filepath.Join(d, "hooks", timing+".hooks"),
		os.O_RDWR|os.O_CREATE|os.O_APPEND,
		0644,
	)
	if err != nil {
		return err
	}
	defer fp.Close()

	_, err = fp.WriteString(h.String() + "\n")
	return err
}

func runTest(timing string, args []string) error {
	progress, err := gitRebaseInProgress()
	if err != nil {
		return err
	}
	if progress {
		fmt.Errorf("rebase in progress, skip %s hooks", timing)
		return nil
	}

	d, err := gitDirPath()
	if err != nil {
		return err
	}

	matches, err := filepath.Glob(filepath.Join(d, "hooks", timing+".installed", "*"))
	if err != nil {
		return err
	}

	for _, script := range matches {
		cmd := exec.Command(script)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		err := cmd.Run()
		if err != nil {
			return err
		}
	}
	return nil
}

func whichEditor() string {
	editor := os.Getenv("EDITOR")
	if len(editor) > 0 {
		return editor
	}

	editor = os.Getenv("VISUAL")
	if len(editor) > 0 {
		return editor
	}

	return "vi"
}

func getModTime(path string) (time.Time, error) {
	st, err := os.Stat(path)

	if err != nil {
		return time.Time{}, err
	} else {
		return st.ModTime(), nil
	}
}

func runEdit(timing string) error {
	d, err := gitDirPath()
	if err != nil {
		return err
	}

	path := filepath.Join(d, "hooks", timing+".hooks")
	prevModTime, err := getModTime(path)

	if err != nil {
		if os.IsNotExist(err) {
			prevModTime = time.Unix(0, 0)
		} else {
			return err
		}
	}

	editor := whichEditor()
	fmt.Printf("run: %s %s\n", editor, path)

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	err = cmd.Run()
	if err != nil {
		return err
	}

	currModTime, err := getModTime(path)
	if err != nil {
		return err
	}

	if currModTime.After(prevModTime) {
		return updateHooks(timing)
	}

	return nil
}

func showHookList(timing string) error {
	d, err := gitDirPath()
	if err != nil {
		return err
	}
	bytes, err := ioutil.ReadFile(filepath.Join(d, "hooks", timing+".hooks"))
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(bytes)
	return err
}

func isCorrectTiming(timing string) bool {
	for _, t := range timings {
		if t == timing {
			return true
		}
	}
	return false
}

func unshiftTiming(c *cli.Context) (string, []string, error) {
	args := c.Args()
	if len(args) == 0 || !isCorrectTiming(args[0]) {
		return "", nil, errors.New("wrong timing")
	}
	return args[0], args[1:], nil
}

func main() {
	app := cli.NewApp()
	app.Version = version
	app.Compiled = compiled
	app.Author = author
	app.Email = email

	app.Commands = []cli.Command{
		{
			Name: "init",
			Action: func(c *cli.Context) {
				err := createRootHook()
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			},
		},
		{
			Name: "install",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  `link, l`,
					Usage: `Create symbolic link instead of copy to install a local script`,
				},
			},
			Action: func(c *cli.Context) {
				timing, args, err := unshiftTiming(c)
				if err != nil || len(args) != 1 {
					cli.ShowAppHelp(c)
					os.Exit(1)
				}
				err = installHook(timing, args[0])
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			},
		},
		{
			Name: "test",
			Action: func(c *cli.Context) {
				timing, args, err := unshiftTiming(c)
				if err != nil {
					cli.ShowAppHelp(c)
					os.Exit(1)
				}
				err = runTest(timing, args)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			},
		},
		{
			Name: "edit",
			Action: func(c *cli.Context) {
				timing, args, err := unshiftTiming(c)
				if err != nil || len(args) > 0 {
					cli.ShowAppHelp(c)
					os.Exit(1)
				}
				err = runEdit(timing)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			},
		},
		{
			Name: "update",
			Action: func(c *cli.Context) {
				timing, args, err := unshiftTiming(c)
				if err != nil || len(args) > 0 {
					cli.ShowAppHelp(c)
					os.Exit(1)
				}
				err = updateHooks(timing)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			},
		},
		{
			Name: "show",
			Action: func(c *cli.Context) {
				timing, args, err := unshiftTiming(c)
				if err != nil || len(args) > 0 {
					cli.ShowAppHelp(c)
					os.Exit(1)
				}
				err = showHookList(timing)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			},
		},
	}

	app.Run(os.Args)
}
