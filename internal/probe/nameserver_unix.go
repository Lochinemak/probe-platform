//go:build !windows

package probe

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

// SystemNameservers lists the nameservers the system resolver is configured
// with, in order.
func SystemNameservers() ([]string, error) {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil, errors.New("cannot read the system resolver config; specify dns_server")
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out, nil
}
