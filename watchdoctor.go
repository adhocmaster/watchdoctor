package watchdoctor

import (
	"fmt"
	"net/http"
  "encoding/json"
  "time"
  "net"
  "strings"

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

// Middleware implements an HTTP handler that writes the
// visitor's IP address to a file or stream.
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

  for url, channel := range m.watcherChannels {
    fmt.Println("Cleaning up routine for ", url)
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
	// switch m.Output {
	// case "stdout":
	// 	m.w = os.Stdout
	// case "stderr":
	// 	m.w = os.Stderr
	// default:
	// 	return fmt.Errorf("Watchdoctor: Provision: an output stream is required")
	// }
	return nil
}

// Validate implements caddy.Validator.
func (m *Middleware) Validate() error {
  // validate notification server
  m.validateNotificationServer()

  // May be this one should go to provision
  m.setUpWatcher()

	// if m.w == nil {
	// 	return fmt.Errorf("no writer")
	// }
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (m Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

  host :=  "127.0.0.1:9000"
  fmt.Println("Pinging server: 127.0.0.1:9000")
  timeout := time.Duration(1 * time.Second)
  _, err := net.DialTimeout("tcp", host, timeout)

  if err != nil {
    fmt.Printf("Down %s with error %s", host, err.Error())
  } else {
    fmt.Printf("%s is healthy", host)

  }

	return next.ServeHTTP(w, r)
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (m *Middleware) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {

  var monitoredServersStr string
  var intervalStr string
  var timeoutStr string
  done := d.Args(&m.DirectiveName, &intervalStr, &timeoutStr, &m.NotificationServer, &monitoredServersStr)

  m.MonitorredServers = strings.Split(monitoredServersStr, " ")
  // m.Interval, err = strconv.Atoi(intervalStr)
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
  fmt.Println(monitoredServersStr)
  fmt.Println(m.MonitorredServers)

	// for d.Next() {
  //   // fmt.Fprintln(os.Stdout, "next arg is: ", d.Val())
  //   fmt.Println("next arg is: ", d.Val())
	// 	// if !d.Args(&m.Output) {
	// 	// 	return d.ArgErr()
	// 	// }
	// }
  if done == false {
    return d.ArgErr()
  }
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

  _, err := net.DialTimeout("tcp", m.NotificationServer, m.Timeout)

  // send a welcome notification to the notification server.

  if err != nil {
    return fmt.Errorf("Notification server unreachable at %s", m.NotificationServer)
  }

  return nil

}


func (m *Middleware) setUpWatcher() {

  for _, url := range m.MonitorredServers {
    fmt.Println("Setting up watcher for ", url)
    // create a channel
    m.watcherChannels[url] = make(chan bool, 1) // needs to be buffered as server may shutdown before any goroutine is created
    // create a goroutine
    go watcher(m, m.watcherChannels[url], url)

  }
}

func (m *Middleware) notifyDown(downServer string) {
  // send a message to notfication server that downServer is down
    fmt.Println("Sending down signal for server", downServer)
}

func watcher(owner *Middleware, stopSignal <-chan bool, url string) {
  for {
    select {
      case <-stopSignal:
        fmt.Println("Stopping watcher for %s", url)
        return
      // case <-time.After(owner.Interval * time.Second):

      case <-time.After(owner.Interval):
        fmt.Println("2 second passed and calling pingServer.")
        err := owner.pingServer(url)
        if err != nil {
          owner.notifyDown(url)
        }
    }
  }
}

func (m *Middleware) pingServer(url string) error {

    fmt.Println("Pinging server: %s", url)

    _, err := net.DialTimeout("tcp", url, m.Timeout)
    if err != nil {
      fmt.Println("Down %s with error %s", url, err.Error())
      return fmt.Errorf("Down %s with error %s", url, err.Error())
    } else {
      fmt.Println("%s is healthy", url)

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
