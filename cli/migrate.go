package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/spf13/cobra"
)

// buildMigrateCmd 提供「只读」的迁移辅助命令。
//
// fxkit 不在应用启动时执行 DDL（生产由你们的 SQL 上线平台负责）：
//   - print：导出框架自有表（outbox / authz / casbin）的 DDL，交给平台审批执行；
//   - check：连库校验 schema 契约，发布顺序错误时 fail fast。
func buildMigrateCmd(opts []di.Option) *cobra.Command {
	var (
		dialect string
		version string
	)

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Print/baseline framework DDL; schema check (DDL execution belongs to Atlas/your platform)",
	}

	printCmd := &cobra.Command{
		Use:   "print",
		Short: "Print framework DDL for a dialect",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sql, err := gormx.PrintMigrations(dialect, strings.TrimSpace(version))
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write([]byte(sql))
			return err
		},
	}
	printCmd.Flags().StringVar(&dialect, "dialect", "postgres", "postgres | mysql | sqlite")
	printCmd.Flags().StringVar(&version, "version", "", "only files with this version prefix (e.g. 0001)")

	var (
		baselineDir     string
		baselineFile    string
		baselineForce   bool
		baselineDialect string
	)
	baselineCmd := &cobra.Command{
		Use:   "baseline",
		Short: "Write framework DDL as the first Atlas migration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d := strings.TrimSpace(baselineDialect)
			if d == "" {
				d = "postgres"
			}
			sql, err := gormx.PrintMigrations(d, "")
			if err != nil {
				return err
			}
			dir := strings.TrimSpace(baselineDir)
			if dir == "" {
				dir = filepath.Join("migrations", d)
			}
			name := strings.TrimSpace(baselineFile)
			if name == "" {
				name = "0001_fxkit_core.sql"
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			path := filepath.Join(dir, name)
			if !baselineForce {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("baseline: %s already exists (use --force to overwrite)", path)
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			if err := os.WriteFile(path, []byte(sql), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	baselineCmd.Flags().StringVar(&baselineDialect, "dialect", "postgres", "postgres | mysql | sqlite")
	baselineCmd.Flags().StringVar(&baselineDir, "dir", "", "target directory (default migrations/<dialect>)")
	baselineCmd.Flags().StringVar(&baselineFile, "filename", "0001_fxkit_core.sql", "baseline file name")
	baselineCmd.Flags().BoolVar(&baselineForce, "force", false, "overwrite an existing file")

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Verify framework tables/columns exist in the configured database",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := newApp(cmd, opts)
			defer di.Shutdown(root)

			client, err := di.Invoke[*gormx.Client](root)
			if err != nil {
				return err
			}
			if client == nil || client.Pool() == nil {
				return fmt.Errorf("migrate check: database is not configured (db.driver / db.dsn)")
			}
			if err := gormx.CheckSchema(cmd.Context(), client.Pool()); err != nil {
				return fmt.Errorf("migrate check: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "schema OK")
			return nil
		},
	}

	cmd.AddCommand(printCmd, baselineCmd, checkCmd)
	return cmd
}
