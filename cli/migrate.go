package cli

import (
	"fmt"
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
		Short: "Print framework DDL / verify schema (DDL execution belongs to your SQL platform)",
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

	cmd.AddCommand(printCmd, checkCmd)
	return cmd
}
