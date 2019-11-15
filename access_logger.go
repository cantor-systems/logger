package logger

import (
	apachelog "github.com/lestrrat-go/apache-logformat"
	"net/http"
	"os"
)

func NewAccessLog(handler http.Handler) http.Handler {
	combinedLog, _ := apachelog.New(`%h %l %{X-Client-Application-Id}o %t "%r" %>s %b "%{Referer}i" "%{User-agent}i"`)
	return combinedLog.Wrap(handler, os.Stderr)
}

func NewAccessLogWithFormat(handler http.Handler, format string) http.Handler {
	combinedLog, _ := apachelog.New(format)
	return combinedLog.Wrap(handler, os.Stderr)
}
