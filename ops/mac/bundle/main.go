// bundle packs the Mac host bundle and writes its signed manifest
// (DECISIONS I-1686; internal/machost has both formats).
//
//	go run ./ops/mac/bundle pack --dir DIR --version V --out FILE.tar.gz
//	go run ./ops/mac/bundle manifest --version V --bundle FILE --url URL --key KEY.pem [--pubkey PUB.pem] > manifest-darwin.json
//
// pack takes DIR/bin/<every machost.BundleFiles entry> and writes a tar
// whose bytes depend on the files alone. manifest signs with the release
// key's private half (the release environment's
// REPOSE_RELEASE_SIGNING_KEY) and checks the result against the public
// half before it prints, so a manifest every Mac would refuse is never
// published.
package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/heracraft/repose/internal/machost"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bundle pack|manifest ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "pack":
		err = pack(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %s", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bundle:", err)
		os.Exit(1)
	}
}

func pack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	dir := fs.String("dir", "", "directory holding bin/")
	version := fs.String("version", "", "bundle version")
	out := fs.String("out", "", "output .tar.gz")
	_ = fs.Parse(args)
	if *dir == "" || *version == "" || *out == "" {
		return errors.New("pack needs --dir, --version and --out")
	}
	f, err := os.Create(*out + ".tmp")
	if err != nil {
		return err
	}
	if err := machost.WriteBundle(f, *dir, *version); err != nil {
		_ = f.Close()
		_ = os.Remove(*out + ".tmp")
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(*out+".tmp", *out)
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	version := fs.String("version", "", "bundle version")
	bundle := fs.String("bundle", "", "the bundle file")
	url := fs.String("url", "", "where the bundle is published")
	keyPath := fs.String("key", "", "the signing key, PEM (EC or PKCS#8)")
	pubPath := fs.String("pubkey", "", "the public key to check against (default the release key)")
	_ = fs.Parse(args)
	if *version == "" || *bundle == "" || *url == "" || *keyPath == "" {
		return errors.New("manifest needs --version, --bundle, --url and --key")
	}
	sum, err := machost.FileSHA256(*bundle)
	if err != nil {
		return err
	}
	kb, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	key, err := parseKey(kb)
	if err != nil {
		return err
	}
	var m machost.Manifest
	m.Version = *version
	m.Darwin.ARM64 = machost.Asset{URL: *url, SHA256: sum}
	digest := sha256.Sum256([]byte(m.SignedText()))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return err
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	pub := machost.ReleasePubKeyPEM
	if *pubPath != "" {
		pb, err := os.ReadFile(*pubPath)
		if err != nil {
			return err
		}
		pub = string(pb)
	}
	if _, err := machost.ParseManifest(b, pub); err != nil {
		return fmt.Errorf("the manifest just signed does not verify: %w", err)
	}
	_, err = fmt.Println(string(b))
	return err
}

func parseKey(b []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("key: no PEM block")
	}
	if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("key: not ECDSA")
	}
	return ek, nil
}
