// Package hostdev is the one-host, one-operator stand-in for the api
// (DECISIONS I-17): it serves the Register, Rotate and Session side of
// docs/interfaces/grpc-hostd.md for exactly one host, keeps its state in
// one JSON file, and exposes every command as a subcommand.
package hostdev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Files under the state directory.
const (
	CAFile        = "ca.pem"
	CAKeyFile     = "ca.key"
	ServerCert    = "server.pem"
	ServerKey     = "server.key"
	SSHCAKey      = "ssh_ca"
	SSHCAPub      = "ssh_ca.pub"
	StateFile     = "state.json"
	ControlSocket = "control.sock"
	LogsDir       = "logs"
)

// Project is one project on the host: one guest.
type Project struct {
	Name        string            `json:"name"`
	ProjectID   string            `json:"project_id"`
	GuestID     string            `json:"guest_id"`
	Class       string            `json:"class"`
	VolumeBytes uint64            `json:"volume_bytes"`
	Closure     string            `json:"system_closure,omitempty"`
	GuestIP     string            `json:"guest_ip,omitempty"`
	VsockCID    uint32            `json:"vsock_cid,omitempty"`
	HostKey     []byte            `json:"host_key,omitempty"`
	HostCert    []byte            `json:"host_cert,omitempty"`
	Secrets     map[string][]byte `json:"secrets,omitempty"`
	Fragment    []byte            `json:"fragment,omitempty"`
	BaseRef     string            `json:"base_ref,omitempty"`
	LastBlob    string            `json:"last_blob_path,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

// CommandRecord is one command sent to the host.
type CommandRecord struct {
	CommandID string          `json:"command_id"`
	Kind      string          `json:"kind"`
	Project   string          `json:"project,omitempty"`
	Command   json.RawMessage `json:"command"`
	Result    json.RawMessage `json:"result,omitempty"`
	SentAt    time.Time       `json:"sent_at"`
	DoneAt    time.Time       `json:"done_at,omitempty"`
}

// HostRecord is what the one host reported.
type HostRecord struct {
	HostID        string            `json:"host_id"`
	Registered    time.Time         `json:"registered_at"`
	Info          json.RawMessage   `json:"info,omitempty"`
	LastHello     json.RawMessage   `json:"last_hello,omitempty"`
	LastHeartbeat json.RawMessage   `json:"last_heartbeat,omitempty"`
	HeartbeatAt   time.Time         `json:"heartbeat_at,omitempty"`
	Guests        map[string]string `json:"guest_states,omitempty"`
}

// State is the JSON file.
type State struct {
	Listen     string                    `json:"listen"`
	GuestCIDR  string                    `json:"guest_cidr"`
	JoinToken  string                    `json:"join_token"`
	TokenUsed  bool                      `json:"token_used"`
	Host       HostRecord                `json:"host"`
	Projects   map[string]*Project       `json:"projects"`
	Commands   map[string]*CommandRecord `json:"commands"`
	Samples    []json.RawMessage         `json:"samples"`
	Events     []json.RawMessage         `json:"events"`
	UserHandle string                    `json:"user_handle"`
	UserID     string                    `json:"user_id"`
	// The hub, when `init` was given one (DECISIONS I-1380): Register
	// then answers the edge's peer, the host's address and a key pair it
	// mints, as the api does, and records the public half here so the
	// test that plays the edge can peer the host (the role wgsync has).
	EdgePubkey   string `json:"edge_pubkey,omitempty"`
	EdgeEndpoint string `json:"edge_endpoint,omitempty"`
	WGAddress    string `json:"wg_address,omitempty"`
	HostWGPubkey string `json:"host_wg_pubkey,omitempty"`
}

// Store is the file with a lock.
type Store struct {
	Dir string
	mu  sync.Mutex
	s   *State
}

// Open loads the state file.
func Open(dir string) (*Store, error) {
	st := &Store{Dir: dir}
	b, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		return nil, err
	}
	st.s = &State{}
	if err := json.Unmarshal(b, st.s); err != nil {
		return nil, err
	}
	if st.s.Projects == nil {
		st.s.Projects = map[string]*Project{}
	}
	if st.s.Commands == nil {
		st.s.Commands = map[string]*CommandRecord{}
	}
	return st, nil
}

// Update mutates the state under the lock and writes it back.
func (st *Store) Update(f func(s *State)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	f(st.s)
	return st.writeLocked()
}

// View reads the state under the lock.
func (st *Store) View(f func(s *State)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	f(st.s)
}

func (st *Store) writeLocked() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(st.Dir, StateFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(st.Dir, StateFile))
}

// Write creates the state file.
func Write(dir string, s *State) error {
	st := &Store{Dir: dir, s: s}
	return st.writeLocked()
}

var pj = protojson.MarshalOptions{UseProtoNames: true}

func rawProto(m interface{ ProtoReflect() protoreflectMessage }) json.RawMessage {
	b, err := pj.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

var _ = hostdv1.File_repose_hostd_v1_hostd_proto
