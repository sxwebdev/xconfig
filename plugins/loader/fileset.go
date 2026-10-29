package loader

import (
	"bytes"
	"errors"
	"io/fs"
	"os"

	"github.com/sxwebdev/xconfig/plugins"
)

// FileSet holds the contents of a Loader's files as read at one moment. Its
// plugins parse exactly these bytes, so a file changing on disk while it is
// being parsed cannot mix two versions into one configuration, and Equal
// compares what was parsed rather than what is on disk now.
type FileSet struct {
	origin *Loader
	files  []fileContent
}

type fileContent struct {
	file   File
	data   []byte
	exists bool
}

// ReadFiles reads every registered file once. A missing optional file is
// recorded as absent; any other read error is returned.
func (f *Loader) ReadFiles() (*FileSet, error) {
	set := &FileSet{
		origin: f,
		files:  make([]fileContent, 0, len(f.files)),
	}

	for _, file := range f.files {
		data, err := os.ReadFile(file.Path)
		if err != nil {
			if file.Optional && errors.Is(err, fs.ErrNotExist) {
				set.files = append(set.files, fileContent{file: file})
				continue
			}
			return nil, err
		}
		set.files = append(set.files, fileContent{file: file, data: data, exists: true})
	}

	return set, nil
}

// Plugins returns one plugin per file, in registration order, that parses the
// content read by ReadFiles. Like the plugins of Loader.Plugins they record
// unknown and present fields in the Loader as they parse, so other plugins of
// the same parse read the fields of these files from it. Plugins clears the
// fields of the previous parse, and restore puts them back for a parse that
// is rejected. Until then, the Loader reports the parse in progress.
func (s *FileSet) Plugins() (ps []plugins.Plugin, restore func()) {
	s.origin.mu.Lock()
	unknown, present := s.origin.unknownFields, s.origin.presentFields
	s.origin.unknownFields = make(map[string][]string)
	s.origin.presentFields = make(map[string]map[string]struct{})
	s.origin.mu.Unlock()

	ps = make([]plugins.Plugin, 0, len(s.files))
	for _, content := range s.files {
		ps = append(ps, &walker{
			filepath:              content.file.Path,
			unmarshal:             content.file.Unmarshal,
			disallowUnknownFields: s.origin.disallowUnknownFields,
			loader:                s.origin,
			data:                  content.data,
			read:                  content.exists,
		})
	}

	restore = func() {
		s.origin.mu.Lock()
		defer s.origin.mu.Unlock()

		s.origin.unknownFields, s.origin.presentFields = unknown, present
	}
	return ps, restore
}

// Paths returns the paths of the files in this set, in registration order.
func (s *FileSet) Paths() []string {
	paths := make([]string, 0, len(s.files))
	for _, content := range s.files {
		paths = append(paths, content.file.Path)
	}

	return paths
}

// Equal reports whether both sets hold the same files with the same contents.
func (s *FileSet) Equal(other *FileSet) bool {
	if s == nil || other == nil {
		return s == other
	}
	if len(s.files) != len(other.files) {
		return false
	}
	for i, content := range s.files {
		o := other.files[i]
		if content.file.Path != o.file.Path || content.exists != o.exists || !bytes.Equal(content.data, o.data) {
			return false
		}
	}

	return true
}
