package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "tgask",
	Short: "A tool for asking questions via Telegram",
}

func Execute(version string) {
	rootCmd.Version = version // enables --version flag automatically
	rootCmd.AddCommand(serveCmd, askCmd, sendCmd)
	if err := rootCmd.Execute(); err != nil {
		// cobra prints the error; just exit
		os.Exit(1)
	}
}
