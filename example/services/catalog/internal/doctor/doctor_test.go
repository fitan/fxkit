package doctor_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/fitan/fxkit-example/services/catalog/internal/doctor"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorCommand(t *testing.T) {
	// 准备临时测试配置 YAML
	cfgContent := `
app:
  name: "catalog-doctor-test"
server:
  port: "9999"
db:
  driver: "sqlite"
discovery:
  consul_address: "http://127.0.0.1:8500"
  register: true
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(tmpFile, []byte(cfgContent), 0o600))

	// 构建父级根命令以模拟全局 --config 参数
	rootCmd := &cobra.Command{Use: "catalog"}
	rootCmd.PersistentFlags().StringP("config", "c", tmpFile, "config file")

	docCmd := doctor.Command()
	rootCmd.AddCommand(docCmd)

	// 捕获 os.Stdout 输出
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	rootCmd.SetArgs([]string{"doctor", "--config", tmpFile})
	execErr := rootCmd.Execute()

	_ = w.Close()
	os.Stdout = oldStdout

	require.NoError(t, execErr)

	outBytes, err := io.ReadAll(r)
	require.NoError(t, err)
	output := string(outBytes)

	assert.Contains(t, output, "app.name=catalog-doctor-test")
	assert.Contains(t, output, "server.port=9999")
	assert.Contains(t, output, "db.driver=sqlite")
	assert.Contains(t, output, "discovery.consul_address=http://127.0.0.1:8500")
	assert.Contains(t, output, "discovery.register=true")
}

func TestDoctorCommand_FileNotFound(t *testing.T) {
	rootCmd := &cobra.Command{Use: "catalog"}
	rootCmd.PersistentFlags().StringP("config", "c", "nonexistent.yaml", "config file")

	docCmd := doctor.Command()
	rootCmd.AddCommand(docCmd)

	rootCmd.SetArgs([]string{"doctor", "--config", "nonexistent.yaml"})
	err := rootCmd.Execute()
	assert.Error(t, err)
}
