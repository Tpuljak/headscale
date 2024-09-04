package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tpuljak/headscale/integration/hsic"
	"github.com/tpuljak/headscale/integration/tsic"
)

func TestResolveMagicDNS(t *testing.T) {
	IntegrationSkip(t)
	t.Parallel()

	scenario, err := NewScenario(dockertestMaxWait())
	assertNoErr(t, err)
	defer scenario.Shutdown()

	spec := map[string]int{
		"magicdns1": len(MustTestVersions),
		"magicdns2": len(MustTestVersions),
	}

	err = scenario.CreateHeadscaleEnv(spec, []tsic.Option{}, hsic.WithTestName("magicdns"))
	assertNoErrHeadscaleEnv(t, err)

	allClients, err := scenario.ListTailscaleClients()
	assertNoErrListClients(t, err)

	err = scenario.WaitForTailscaleSync()
	assertNoErrSync(t, err)

	// assertClientsState(t, allClients)

	// Poor mans cache
	_, err = scenario.ListTailscaleClientsFQDNs()
	assertNoErrListFQDN(t, err)

	_, err = scenario.ListTailscaleClientsIPs()
	assertNoErrListClientIPs(t, err)

	for _, client := range allClients {
		for _, peer := range allClients {
			// It is safe to ignore this error as we handled it when caching it
			peerFQDN, _ := peer.FQDN()

			assert.Equal(t, fmt.Sprintf("%s.headscale.net", peer.Hostname()), peerFQDN)

			command := []string{
				"tailscale",
				"ip", peerFQDN,
			}
			result, _, err := client.Execute(command)
			if err != nil {
				t.Fatalf(
					"failed to execute resolve/ip command %s from %s: %s",
					peerFQDN,
					client.Hostname(),
					err,
				)
			}

			ips, err := peer.IPs()
			if err != nil {
				t.Fatalf(
					"failed to get ips for %s: %s",
					peer.Hostname(),
					err,
				)
			}

			for _, ip := range ips {
				if !strings.Contains(result, ip.String()) {
					t.Fatalf("ip %s is not found in \n%s\n", ip.String(), result)
				}
			}
		}
	}
}

// TestValidateResolvConf validates that the resolv.conf file
// ends up as expected in our Tailscale containers.
// All the containers are based on Alpine, meaning Tailscale
// will overwrite the resolv.conf file.
// On other platform, Tailscale will integrate with a dns manager
// if available (like Systemd-Resolved).
func TestValidateResolvConf(t *testing.T) {
	IntegrationSkip(t)

	resolvconf := func(conf string) string {
		return strings.ReplaceAll(`# resolv.conf(5) file generated by tailscale
# For more info, see https://tailscale.com/s/resolvconf-overwrite
# DO NOT EDIT THIS FILE BY HAND -- CHANGES WILL BE OVERWRITTEN
`+conf, "\t", "")
	}

	tests := []struct {
		name                string
		conf                map[string]string
		wantConfCompareFunc func(*testing.T, string)
	}{
		// New config
		{
			name: "no-config",
			conf: map[string]string{
				"HEADSCALE_DNS_BASE_DOMAIN":        "",
				"HEADSCALE_DNS_MAGIC_DNS":          "false",
				"HEADSCALE_DNS_NAMESERVERS_GLOBAL": "",
			},
			wantConfCompareFunc: func(t *testing.T, got string) {
				assert.NotContains(t, got, "100.100.100.100")
			},
		},
		{
			name: "global-only",
			conf: map[string]string{
				"HEADSCALE_DNS_BASE_DOMAIN":        "",
				"HEADSCALE_DNS_MAGIC_DNS":          "false",
				"HEADSCALE_DNS_NAMESERVERS_GLOBAL": "8.8.8.8 1.1.1.1",
			},
			wantConfCompareFunc: func(t *testing.T, got string) {
				want := resolvconf(`
					nameserver 100.100.100.100
				`)
				assert.Equal(t, want, got)
			},
		},
		{
			name: "base-integration-config",
			conf: map[string]string{
				"HEADSCALE_DNS_BASE_DOMAIN": "very-unique-domain.net",
			},
			wantConfCompareFunc: func(t *testing.T, got string) {
				want := resolvconf(`
					nameserver 100.100.100.100
					search very-unique-domain.net
				`)
				assert.Equal(t, want, got)
			},
		},
		{
			name: "base-magic-dns-off",
			conf: map[string]string{
				"HEADSCALE_DNS_MAGIC_DNS":   "false",
				"HEADSCALE_DNS_BASE_DOMAIN": "very-unique-domain.net",
			},
			wantConfCompareFunc: func(t *testing.T, got string) {
				want := resolvconf(`
					nameserver 100.100.100.100
					search very-unique-domain.net
				`)
				assert.Equal(t, want, got)
			},
		},
		{
			name: "base-extra-search-domains",
			conf: map[string]string{
				"HEADSCALE_DNS_SEARCH_DOMAINS": "test1.no test2.no",
				"HEADSCALE_DNS_BASE_DOMAIN":    "with-local-dns.net",
			},
			wantConfCompareFunc: func(t *testing.T, got string) {
				want := resolvconf(`
					nameserver 100.100.100.100
					search with-local-dns.net test1.no test2.no
				`)
				assert.Equal(t, want, got)
			},
		},
		{
			name: "base-nameservers-split",
			conf: map[string]string{
				"HEADSCALE_DNS_NAMESERVERS_SPLIT": `{foo.bar.com: ["1.1.1.1"]}`,
				"HEADSCALE_DNS_BASE_DOMAIN":       "with-local-dns.net",
			},
			wantConfCompareFunc: func(t *testing.T, got string) {
				want := resolvconf(`
					nameserver 100.100.100.100
					search with-local-dns.net
				`)
				assert.Equal(t, want, got)
			},
		},
		{
			name: "base-full-no-magic",
			conf: map[string]string{
				"HEADSCALE_DNS_MAGIC_DNS":          "false",
				"HEADSCALE_DNS_BASE_DOMAIN":        "all-of.it",
				"HEADSCALE_DNS_NAMESERVERS_GLOBAL": `8.8.8.8`,
				"HEADSCALE_DNS_SEARCH_DOMAINS":     "test1.no test2.no",
				// TODO(kradalby): this currently isnt working, need to fix it
				// "HEADSCALE_DNS_NAMESERVERS_SPLIT": `{foo.bar.com: ["1.1.1.1"]}`,
				// "HEADSCALE_DNS_EXTRA_RECORDS":     `[{ name: "prometheus.myvpn.example.com", type: "A", value: "100.64.0.4" }]`,
			},
			wantConfCompareFunc: func(t *testing.T, got string) {
				want := resolvconf(`
					nameserver 100.100.100.100
					search all-of.it test1.no test2.no
				`)
				assert.Equal(t, want, got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenario, err := NewScenario(dockertestMaxWait())
			assertNoErr(t, err)
			defer scenario.Shutdown()

			spec := map[string]int{
				"resolvconf1": 3,
				"resolvconf2": 3,
			}

			err = scenario.CreateHeadscaleEnv(spec, []tsic.Option{}, hsic.WithTestName("resolvconf"), hsic.WithConfigEnv(tt.conf))
			assertNoErrHeadscaleEnv(t, err)

			allClients, err := scenario.ListTailscaleClients()
			assertNoErrListClients(t, err)

			err = scenario.WaitForTailscaleSync()
			assertNoErrSync(t, err)

			// Poor mans cache
			_, err = scenario.ListTailscaleClientsFQDNs()
			assertNoErrListFQDN(t, err)

			_, err = scenario.ListTailscaleClientsIPs()
			assertNoErrListClientIPs(t, err)

			time.Sleep(30 * time.Second)

			for _, client := range allClients {
				b, err := client.ReadFile("/etc/resolv.conf")
				assertNoErr(t, err)

				t.Logf("comparing resolv conf of %s", client.Hostname())
				tt.wantConfCompareFunc(t, string(b))
			}
		})
	}

}
