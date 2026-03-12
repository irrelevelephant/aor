package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
	"aor/ata/web"
)

func Serve(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 4400, "HTTP port")
	addr := fs.String("addr", "0.0.0.0", "Listen address")
	tlsCert := fs.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := fs.String("tls-key", "", "Path to TLS private key file")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if (*tlsCert == "") != (*tlsKey == "") {
		return fmt.Errorf("-tls-cert and -tls-key must both be provided")
	}

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	if *tlsCert != "" {
		fmt.Printf("ata web UI: https://localhost:%d\n", *port)
	} else {
		fmt.Printf("ata web UI: http://localhost:%d\n", *port)
	}
	return web.Serve(d, listen, *tlsCert, *tlsKey)
}
