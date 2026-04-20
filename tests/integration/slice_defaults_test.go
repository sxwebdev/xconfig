package integration_test

import (
	"os"
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/decoders/xconfigyaml"
	"github.com/sxwebdev/xconfig/internal/testutil"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

type sliceGroup struct {
	IsEnabled bool   `yaml:"is_enabled" default:"true"`
	Name      string `yaml:"name"`
	Port      int    `yaml:"port" default:"8080"`
}

type sliceRoot struct {
	Groups []sliceGroup `yaml:"groups"`
}

func TestSliceDefaults_ViaXconfigLoad_Yaml(t *testing.T) {
	yamlContent := `groups:
  - name: a
  - name: b
    port: 9090
  - name: c
    is_enabled: false
`

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
	})
	if err != nil {
		t.Fatal(err)
	}

	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.AddFile(path, false); err != nil {
		t.Fatal(err)
	}

	cfg := &sliceRoot{}
	_, err = xconfig.Load(cfg,
		xconfig.WithLoader(l),
		xconfig.WithSkipEnv(),
		xconfig.WithSkipFlags(),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := &sliceRoot{
		Groups: []sliceGroup{
			{IsEnabled: true, Name: "a", Port: 8080},
			{IsEnabled: true, Name: "b", Port: 9090},
			// is_enabled:false was present in the file — default must NOT
			// overwrite it (rescan consults loader.PresentFields).
			{IsEnabled: false, Name: "c", Port: 8080},
		},
	}

	testutil.Equal(t, want, cfg)
}
