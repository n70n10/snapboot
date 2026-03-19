package logger

import (
	"fmt"
	"os"
)

const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	bold   = "\033[1m"
)

func Info(format string, args ...any) {
	fmt.Fprintf(os.Stdout, green+"[snapboot]"+reset+" "+format+"\n", args...)
}

func Warn(format string, args ...any) {
	fmt.Fprintf(os.Stdout, yellow+"[snapboot]"+reset+" "+format+"\n", args...)
}

func Error(format string, args ...any) {
	fmt.Fprintf(os.Stderr, red+"[snapboot] ERROR"+reset+" "+format+"\n", args...)
}

func Fatal(format string, args ...any) {
	Error(format, args...)
	os.Exit(1)
}

func Header(format string, args ...any) {
	fmt.Fprintf(os.Stdout, bold+format+reset+"\n", args...)
}
