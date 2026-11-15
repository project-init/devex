package db

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

//go:embed templates/*
var templates embed.FS

// nonOverwriteFiles are the generated files a team edits, kept when they exist unless forced.
var nonOverwriteFiles = []string{
	"init/100-bootstrap.sql",
	"init/200-schema.sql",
	"init/300-users.sql",
	"seeddata/seed.sql",
	"seeddata/users.csv",
	"sqlc/users.sql",
	"sqlc.yaml",
	"structure.sql",
}

// Action is what generation does to one file.
type Action string

const (
	ActionCreate    Action = "create"    // the file is new
	ActionOverwrite Action = "overwrite" // the file exists and generation replaces it
	ActionSkip      Action = "skip"      // the file exists and holds the team's edits
)

// File is one generated file and what generation does to it.
type File struct {
	Path    string `json:"path"`
	Action  Action `json:"action"`
	content []byte
}

// Plan validates config, renders every template into memory, and decides what happens to each
// file under outputDirectory. It writes nothing, so a template error leaves no partial file.
// force overwrites the files a team edits.
func Plan(config Config, outputDirectory string, force bool) ([]File, error) {
	return plan(templates, "templates", config, outputDirectory, force)
}

func plan(source fs.FS, root string, config Config, outputDirectory string, force bool) ([]File, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var files []File
	// fs.WalkDir names entries with forward slashes, as embed.FS requires on every platform.
	err := fs.WalkDir(source, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative := strings.TrimSuffix(strings.TrimPrefix(name, root+"/"), ".tmpl")
		content, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".tmpl") {
			if content, err = render(relative, content, config); err != nil {
				return fmt.Errorf("generate %s: %w", relative, err)
			}
		}
		file := File{Path: filepath.Join(outputDirectory, filepath.FromSlash(relative)), Action: ActionCreate, content: content}
		switch _, err := os.Stat(file.Path); {
		case err == nil && !force && slices.Contains(nonOverwriteFiles, relative):
			file.Action = ActionSkip
		case err == nil:
			file.Action = ActionOverwrite
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func render(name string, text []byte, config Config) ([]byte, error) {
	tmpl, err := template.New(name).Parse(string(text))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, config); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Write writes every planned file that is not skipped, in place, so a replaced file keeps its
// mode, owner, and links. A file the user cannot write stops the run at that file.
func Write(files []File) error {
	for _, file := range files {
		if file.Action == ActionSkip {
			continue
		}
		if err := writeFile(file); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
	}
	return nil
}

func writeFile(file File) error {
	if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
		return err
	}
	// A new file gets the mode os.Create gives it, under the umask.
	out, err := os.OpenFile(file.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	_, err = out.Write(file.content)
	if err == nil && strings.HasSuffix(file.Path, ".sh") {
		err = makeExecutable(out)
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

// makeExecutable adds execute wherever the script has read, including to one an older devex
// wrote without it.
func makeExecutable(script *os.File) error {
	info, err := script.Stat()
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if executable := mode | (mode&0o444)>>2; executable != mode {
		return script.Chmod(executable)
	}
	return nil
}
