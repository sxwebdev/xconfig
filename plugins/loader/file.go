// Package file provides config loader support for xconfig
package loader

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sxwebdev/xconfig/plugins"
)

// Unmarshal is any function that maps the source bytes to the provided
// config.
type Unmarshal func(src []byte, v any) error

type File struct {
	Path      string
	Unmarshal Unmarshal
	Optional  bool
}

// Loader represents a set of file paths and the appropriate
// unmarshal function for the given file.
type Loader struct {
	decoders              map[string]Unmarshal
	files                 []File
	disallowUnknownFields bool
	unknownFields         map[string][]string // filepath -> unknown fields
}

func NewLoader(decoders map[string]Unmarshal) (*Loader, error) {
	l := &Loader{
		decoders:      make(map[string]Unmarshal),
		files:         make([]File, 0),
		unknownFields: make(map[string][]string),
	}

	for format, decoder := range decoders {
		if err := l.RegisterDecoder(format, decoder); err != nil {
			return nil, fmt.Errorf("failed to register decoder for format %q: %w", format, err)
		}
	}

	return l, nil
}

// AddFile appends a new file to the list of files.
func (f *Loader) AddFile(path string, optional bool) error {
	if path == "" {
		return nil
	}

	fileExt := strings.TrimPrefix(filepath.Ext(path), ".")

	decoder, ok := f.decoders[fileExt]
	if !ok {
		return fmt.Errorf("no decoder registered for format %q", fileExt)
	}

	f.files = append(f.files, File{path, decoder, optional})

	return nil
}

// AddFiles appends multiple files to the list of files.
func (f *Loader) AddFiles(paths []string, optional bool) error {
	if len(paths) == 0 {
		return nil
	}
	for _, path := range paths {
		if err := f.AddFile(path, optional); err != nil {
			return fmt.Errorf("failed to add file %q: %w", path, err)
		}
	}
	return nil
}

// RegisterDecoder registers a new decoder for the given format.
func (f *Loader) RegisterDecoder(format string, decoder Unmarshal) error {
	if format == "" {
		return errors.New("format cannot be empty")
	}

	if f.decoders == nil {
		f.decoders = make(map[string]Unmarshal)
	}

	format = strings.TrimPrefix(format, ".")

	if _, ok := f.decoders[format]; ok {
		return fmt.Errorf("decoder for format %q already registered", format)
	}

	f.decoders[format] = decoder

	return nil
}

// DisallowUnknownFields enables strict validation of configuration files.
// When enabled, loading will fail if any unknown fields are found.
func (f *Loader) DisallowUnknownFields(disallow bool) {
	f.disallowUnknownFields = disallow
}

// GetUnknownFields returns all unknown fields found in configuration files.
// Returns a map where keys are file paths and values are slices of unknown field paths.
func (f *Loader) GetUnknownFields() map[string][]string {
	if f.unknownFields == nil {
		return make(map[string][]string)
	}

	// Return a copy to prevent external modifications
	result := make(map[string][]string, len(f.unknownFields))
	for k, v := range f.unknownFields {
		fields := make([]string, len(v))
		copy(fields, v)
		result[k] = fields
	}

	return result
}

// ClearUnknownFields clears the list of unknown fields.
func (f *Loader) ClearUnknownFields() {
	f.unknownFields = make(map[string][]string)
}

// Plugins constructs a slice of Plugin from the Files list of
// paths and unmarshal functions.
func (f *Loader) Plugins() []plugins.Plugin {
	ps := make([]plugins.Plugin, 0, len(f.files))
	for _, file := range f.files {
		fp := New(
			file.Path,
			file.Unmarshal,
			Config{
				Optional:              file.Optional,
				DisallowUnknownFields: f.disallowUnknownFields,
			},
			f,
		)

		ps = append(ps, fp)
	}

	return ps
}

// NewReader returns a xconfig plugin that unmarshals the content of
// the provided io.Reader into the config using the provided unmarshal
// function. The src will be closed if it is an io.Closer.
func NewReader(src io.Reader, unmarshal Unmarshal) plugins.Plugin {
	return &walker{
		src:       src,
		unmarshal: unmarshal,
	}
}

// Config describes the options required for a file.
type Config struct {
	// indicates if a file that does not exist should be ignored.
	Optional bool
	// indicates if unknown fields should cause an error.
	DisallowUnknownFields bool
}

// New returns an EnvSet.
func New(path string, unmarshal Unmarshal, config Config, loader *Loader) plugins.Plugin {
	plug := &walker{
		filepath:              path,
		unmarshal:             unmarshal,
		disallowUnknownFields: config.DisallowUnknownFields,
		loader:                loader,
	}

	src, err := os.Open(path)

	if err == nil {
		plug.src = src
	}

	if config.Optional && os.IsNotExist(err) {
		err = nil
	}

	plug.err = err

	return plug
}

type walker struct {
	filepath              string
	src                   io.Reader
	conf                  any
	unmarshal             Unmarshal
	disallowUnknownFields bool
	loader                *Loader

	err error
}

func (v *walker) Walk(conf any) error {
	if v.err != nil {
		return v.err
	}

	v.conf = conf
	return v.err
}

func (v *walker) Parse() error {
	if v.err != nil {
		return v.err
	}

	if v.src == nil {
		return nil
	}

	src, err := io.ReadAll(v.src)
	if err != nil {
		return err
	}

	if closer, ok := v.src.(io.Closer); ok {
		err := closer.Close()
		if err != nil {
			return err
		}
	}

	// Check for unknown fields if validation is enabled
	if v.disallowUnknownFields || v.loader != nil {
		unknownFields, err := findUnknownFields(src, v.conf, v.filepath, v.unmarshal)
		if err != nil {
			// If we can't validate, just continue with unmarshaling
			// This allows non-JSON formats to work
		} else if len(unknownFields) > 0 {
			// Store unknown fields in loader
			if v.loader != nil {
				v.loader.unknownFields[v.filepath] = unknownFields
			}

			// Return error if disallowed
			if v.disallowUnknownFields {
				return &UnknownFieldsError{
					Fields: map[string][]string{
						v.filepath: unknownFields,
					},
				}
			}
		}
	}

	return v.unmarshal(src, v.conf)
}
