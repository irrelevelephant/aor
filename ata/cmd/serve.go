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

	if err := fs.Parse(args); err != nil {
		return err
	}

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	fmt.Printf("ata web UI: http://localhost:%d\n", *port)
	return web.Serve(d, listen)
}
