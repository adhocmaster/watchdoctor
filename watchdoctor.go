package watchdoctor

import (
	"fmt"
	"net/http"
  "encoding/json"
  "time"
  "net"
  "strings"
	"bytes"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
  "go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(Middleware{})
	httpcaddyfile.RegisterHandlerDirective("watch_doctor", parseCaddyfile)
}

type Notification struct {
	Type string `json:"type"`
	Server string `json:"server"`
	TimeChecked time.Time
}

// Middleware implements an HTTP handler
type Middleware struct {
	// The file or stream to write to. Can be "stdout"
	// or "stderr".
  DirectiveName string `json:"DirectiveName"`
	Interval time.Duration `json:"Interval"`
	Timeout time.Duration `json:"Timeout"`
	NotificationServer string `json:"NotificationServer"`
	MonitorredServers []string `json:"MonitorredServers"`

  ctx caddy.Context
  logger *zap.Logger
  watcherChannels map[string](chan bool)
}

// CaddyModule returns the Caddy module information.
func (Middleware) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.watchdoctor",
		New: func() caddy.Module { return new(Middleware) },
	}
}

func (m *Middleware) Cleanup() error {

  for _, channel := range m.watcherChannels {
    // fmt.Println("Cleaning up routine for ", url)
    channel <- true

  }
  return nil
}

// Provision implements caddy.Provisioner.
func (m *Middleware) Provision(ctx caddy.Context) error {

  m.ctx = ctx
  m.logger = ctx.Logger(m)
  m.watcherChannels = make(map[string](chan bool))

  fmt.Println("Watchdoctor: Provision: Your module properties are:")
  b, _ := json.Marshal(m)
  fmt.Println(string(b))
	return nil
}

// Validate implements caddy.Validator.
func (m *Middleware) Validate() error {
  // validate notification server
  m.validateNotificationServer()

  // May be this one should go to provision
  m.setUpWatcher()

	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (m Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// remove this interface. But which package should we add it to?

	return next.ServeHTTP(w, r)
}


func (m *Middleware) getTCPURL(originalURL string) string {

		url := strings.Replace(originalURL, "https://", "", 1)
		url = strings.Replace(url, "http://", "", 1)
		url = strings.TrimSpace(url)
		return url

}

func (m *Middleware) parseServersToMonitor(serversStr string) []string {

	// fmt.Println(serversStr)

	servers := strings.Split(serversStr, " ")
	sanitizedServers := make([]string, 0, len(servers))
	for _, url := range servers {
		sanitizeUrl :=  m.getTCPURL(url)

		// fmt.Printf("\nparsed url is %s with len %d", sanitizeUrl, len(sanitizeUrl))
		if len(sanitizeUrl) > 0 {
			// fmt.Printf("\nadding server %s with len %d", sanitizeUrl, len(sanitizeUrl))
			sanitizedServers = append(sanitizedServers, sanitizeUrl)
		}
	}
	return sanitizedServers

}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (m *Middleware) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {

  var monitoredServersStr string
  var intervalStr string
  var timeoutStr string

  done := d.Args(&m.DirectiveName, &intervalStr, &timeoutStr, &m.NotificationServer, &monitoredServersStr)
  if done == false {
    return d.ArgErr()
  }

	m.NotificationServer = strings.TrimSpace(m.NotificationServer)
  m.MonitorredServers = m.parseServersToMonitor(monitoredServersStr)

  interval, err := time.ParseDuration(intervalStr)
  if err != nil {
    return err
  }
  m.Interval = interval

  timeout, err := time.ParseDuration(timeoutStr)
  if err != nil {
    return err
  }
  m.Timeout = timeout

  // fmt.Println(monitoredServersStr)
  // fmt.Println(m.MonitorredServers)

	return nil
}

// parseCaddyfile unmarshals tokens from h into a new Middleware.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m Middleware
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return m, err
}


func (m *Middleware) validateNotificationServer() error {

  if m.NotificationServer == "" {
    m.logger.Error("No notification server given!")
    return fmt.Errorf("No notification server given!")
  }

  // _, err := net.DialTimeout("tcp", m.NotificationServer, m.Timeout)

  // send a welcome notification to the notification server.

	err := m.pingServer(m.getTCPURL(m.NotificationServer))

  if err != nil {
    return fmt.Errorf("Notification server unreachable at %s", m.NotificationServer)
  }

  return nil

}


func (m *Middleware) setUpWatcher() {
	m.logger.Debug("Setting up watchers for Backend servers")
  for _, url := range m.MonitorredServers {
    // fmt.Println("Setting up watcher for ", url)
    // create a channel
    m.watcherChannels[url] = make(chan bool, 1) // needs to be buffered as server may shutdown before any goroutine is created
    // create a goroutine
    go watcher(m, m.watcherChannels[url], url)

  }
}

func (m *Middleware) notifyDown(downServer string) {
  // send a message to notfication server that downServer is down
    // fmt.Println("\nnotifyDown: Sending down signal for server", downServer)
		m.logger.Error("Backend server down", zap.String("server", downServer))
		message := Notification{
			Type: "Down",
			Server: downServer,
			TimeChecked: time.Now(),
		}
		messageBytes, err := json.Marshal(message)
	  // fmt.Println(string(messageBytes))

		if err == nil {
			// # send the message to notification server
			requestBody := bytes.NewBuffer(messageBytes)
			client := http.Client{Timeout: m.Timeout}
			resp, err := client.Post(m.NotificationServer, "application/json", requestBody)
			if err != nil {
				m.logger.Error("Error occurred during sending notification", zap.String("error", err.Error()))
				return
			}
			defer resp.Body.Close()

		} else {
			m.logger.Error("Cannot marshal notification", zap.String("error", err.Error()))
		}

}

func watcher(owner *Middleware, stopSignal <-chan bool, url string) {
  for {
    select {
      case <-stopSignal:
        // fmt.Printf("\nStopping watcher for %s", url)
        return
      // case <-time.After(owner.Interval * time.Second):

      case <-time.After(owner.Interval):
        // fmt.Println("2 second passed and calling pingServer.")
        err := owner.pingServer(url)
        if err != nil {
          owner.notifyDown(url)
        }
    }
  }
}

func (m *Middleware) pingServer(url string) error {

    // fmt.Printf("\nPinging server: %s\n", url)

    _, err := net.DialTimeout("tcp", url, m.Timeout)
    if err != nil {
      // fmt.Printf("\nDown %s with error %s", url, err.Error())
      return fmt.Errorf("\nDown %s with error %s", url, err.Error())
    } else {
      // fmt.Printf("\n%s is healthy", url)

    }
    return nil
}

// Interface guards
var (
	_ caddy.Provisioner           = (*Middleware)(nil)
	_ caddy.Validator             = (*Middleware)(nil)
	_ caddyhttp.MiddlewareHandler = (*Middleware)(nil)
	_ caddyfile.Unmarshaler       = (*Middleware)(nil)
)
