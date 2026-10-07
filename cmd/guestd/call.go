package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/vsockrpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// callUsage lists the request names `guestd call` accepts. They are the
// requests of docs/interfaces/vsock-guestd.md in lower-kebab.
const callUsage = `usage: guestd call <request> [json] [flags]

Sends one request to a running guestd and prints the response as JSON. It is
the client side of docs/interfaces/vsock-guestd.md, for the NixOS VM test and
for an operator on a guest that has lost hostd (ops/RUNBOOK.md, "Guest
unresponsive").

Requests: ping, freeze, thaw, switch, register-paths, grow-fs, write-secrets,
          set-principals, setup-project, sample, exec, shutdown

Examples:
  guestd call ping
  guestd call switch '{"systemClosure":"/nix/store/...-nixos-system"}'
  guestd call exec '{"argv":["uptime"],"timeoutS":5}'
  guestd call freeze --watch 15s      # then print notifications for 15 s
`

// builders maps a request name to a decoder for its JSON body.
var builders = map[string]func(string) (*guestdv1.Request, error){
	"ping": func(string) (*guestdv1.Request, error) {
		return &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}}, nil
	},
	"freeze": func(string) (*guestdv1.Request, error) {
		return &guestdv1.Request{Req: &guestdv1.Request_Freeze{Freeze: &guestdv1.Freeze{}}}, nil
	},
	"thaw": func(string) (*guestdv1.Request, error) {
		return &guestdv1.Request{Req: &guestdv1.Request_Thaw{Thaw: &guestdv1.Thaw{}}}, nil
	},
	"grow-fs": func(string) (*guestdv1.Request, error) {
		return &guestdv1.Request{Req: &guestdv1.Request_GrowFs{GrowFs: &guestdv1.GrowFs{}}}, nil
	},
	"sample": func(string) (*guestdv1.Request, error) {
		return &guestdv1.Request{Req: &guestdv1.Request_Sample{Sample: &guestdv1.Sample{}}}, nil
	},
	"switch": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.Switch{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_Switch{Switch: m}}, nil
	},
	"register-paths": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.RegisterPaths{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_RegisterPaths{RegisterPaths: m}}, nil
	},
	"write-secrets": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.WriteSecrets{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_WriteSecrets{WriteSecrets: m}}, nil
	},
	"set-principals": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.SetPrincipals{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_SetPrincipals{SetPrincipals: m}}, nil
	},
	"setup-project": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.SetupProject{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_SetupProject{SetupProject: m}}, nil
	},
	"exec": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.Exec{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_Exec{Exec: m}}, nil
	},
	"answer-question": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.AnswerQuestion{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_AnswerQuestion{AnswerQuestion: m}}, nil
	},
	"shutdown": func(body string) (*guestdv1.Request, error) {
		m := &guestdv1.Shutdown{}
		if err := unmarshal(body, m); err != nil {
			return nil, err
		}
		return &guestdv1.Request{Req: &guestdv1.Request_Shutdown{Shutdown: m}}, nil
	},
}

func unmarshal(body string, into proto.Message) error {
	if strings.TrimSpace(body) == "" {
		body = "{}"
	}
	if err := protojson.Unmarshal([]byte(body), into); err != nil {
		return fmt.Errorf("parse the request body: %w", err)
	}
	return nil
}

// runCall is `guestd call`.
func runCall(args []string) error {
	fs := flag.NewFlagSet("guestd call", flag.ContinueOnError)
	var (
		socket = fs.String("dev-socket", "/run/repose/guestd.sock", "guestd's unix socket")
		cid    = fs.Uint("cid", 0, "connect over vsock to this CID instead of the socket")
		port   = fs.Uint("vsock-port", uint(vsockrpc.Port), "vsock port")
		watch  = fs.Duration("watch", 0, "after the response, print notifications for this long")
		wait   = fs.Duration("timeout", 30*time.Second, "how long to wait for the response")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, callUsage)
		fs.PrintDefaults()
	}

	// The request name and its optional JSON body come before the flags.
	var positional []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional = append(positional, args[0])
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(positional) == 0 {
		fs.Usage()
		return errors.New("no request named")
	}
	name := positional[0]
	body := "{}"
	if len(positional) > 1 {
		body = positional[1]
	}
	build, ok := builders[name]
	if !ok {
		return fmt.Errorf("unknown request %q; run `guestd call` for the list", name)
	}
	req, err := build(body)
	if err != nil {
		return err
	}

	var conn net.Conn
	if *cid != 0 {
		conn, err = vsockrpc.Dial(uint32(*cid), uint32(*port))
	} else {
		conn, err = vsockrpc.DialUnix(*socket)
	}
	if err != nil {
		return err
	}

	notifications := make(chan string, 64)
	client := vsockrpc.NewClient(conn, func(n *guestdv1.Notify) {
		b, err := protojson.Marshal(n)
		if err != nil {
			return
		}
		select {
		case notifications <- string(b):
		default:
		}
	})
	defer client.Close() //nolint:errcheck // the process is exiting

	ctx, cancel := context.WithTimeout(context.Background(), *wait)
	defer cancel()
	resp, err := client.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("call %s: %w", name, err)
	}
	out, err := protojson.MarshalOptions{Multiline: true}.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode the response: %w", err)
	}
	fmt.Println(string(out))

	if *watch > 0 {
		deadline := time.After(*watch)
		for {
			select {
			case <-deadline:
				return exitFor(resp)
			case n := <-notifications:
				fmt.Println(n)
			}
		}
	}
	return exitFor(resp)
}

// exitFor turns a failed response into a non-zero exit, so a shell test can
// assert on it without parsing JSON.
func exitFor(resp *guestdv1.Response) error {
	if resp.GetOk() {
		return nil
	}
	return fmt.Errorf("%s: %s", resp.GetError().GetCode(), resp.GetError().GetMessage())
}
