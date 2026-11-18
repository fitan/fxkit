package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// DoctorCommand 是一个宿主 Cobra 扩展命令（cli.AddCommand），它读取 YAML
// 而不启动 HTTP 监听器。
func DoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Print app.name / db / discovery from --config (no HTTP listen)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := cmd.Root().PersistentFlags().GetString("config")
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("doctor: read %s: %w", path, err)
			}
			var doc struct {
				App struct {
					// Name 是 app.name。
					Name string `yaml:"name"`
				} `yaml:"app"`
				Server struct {
					// Port 是 server.port。
					Port string `yaml:"port"`
				} `yaml:"server"`
				DB struct {
					// Driver 是 db.driver。
					Driver string `yaml:"driver"`
				} `yaml:"db"`
				Discovery struct {
					// ConsulAddress 是 discovery.consul_address。
					ConsulAddress string `yaml:"consul_address"`
					// Register 是 discovery.register。
					Register bool `yaml:"register"`
				} `yaml:"discovery"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				return fmt.Errorf("doctor: yaml: %w", err)
			}
			if doc.App.Name == "" {
				return fmt.Errorf("doctor: app.name is empty")
			}
			fmt.Printf("app.name=%s\n", doc.App.Name)
			fmt.Printf("server.port=%s\n", doc.Server.Port)
			fmt.Printf("db.driver=%s\n", doc.DB.Driver)
			fmt.Printf("discovery.consul_address=%s\n", doc.Discovery.ConsulAddress)
			fmt.Printf("discovery.register=%v\n", doc.Discovery.Register)
			return nil
		},
	}
}
