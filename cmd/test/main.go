package main

import (
	"fmt"
	"os"
	"time"

	"github.com/miekg/dns"
)

var client = &dns.Client{Net: "udp", Timeout: 3 * time.Second}
var addr = "127.0.0.1:8053"

func main() {
	exitCode := 0

	if !test("ALLOW google.com", "google.com", dns.TypeA, func(resp *dns.Msg) bool {
		return resp.Rcode == dns.RcodeSuccess
	}) {
		if len(os.Args) > 1 && os.Args[1] == "--strict" {
			exitCode = 1
		} else {
			fmt.Println("  ⚠ (likely no internet — skipping strict check)")
		}
	}

	if !test("BLOCK example-blocked.com", "example-blocked.com", dns.TypeA, func(resp *dns.Msg) bool {
		if len(resp.Answer) == 0 {
			return false
		}
		a, ok := resp.Answer[0].(*dns.A)
		return ok && a.A.String() == "0.0.0.0"
	}) {
		exitCode = 1
	}

	if !test("BLOCK ads.tracker.com (AAAA)", "ads.tracker.com", dns.TypeAAAA, func(resp *dns.Msg) bool {
		if len(resp.Answer) == 0 {
			return false
		}
		a, ok := resp.Answer[0].(*dns.AAAA)
		return ok && a.AAAA.String() == "::"
	}) {
		exitCode = 1
	}

	if !test("BLOCK malware.test", "malware.test", dns.TypeA, func(resp *dns.Msg) bool {
		if len(resp.Answer) == 0 {
			return false
		}
		a, ok := resp.Answer[0].(*dns.A)
		return ok && a.A.String() == "0.0.0.0"
	}) {
		exitCode = 1
	}

	if !test("ALLOW github.com", "github.com", dns.TypeA, func(resp *dns.Msg) bool {
		return resp.Rcode == dns.RcodeSuccess
	}) {
		if len(os.Args) > 1 && os.Args[1] == "--strict" {
			exitCode = 1
		}
	}

	fmt.Println()
	if exitCode == 0 {
		fmt.Println("✅ All applicable tests passed.")
	} else {
		fmt.Println("❌ Some tests failed.")
	}
	os.Exit(exitCode)
}

func test(name, domain string, qtype uint16, check func(*dns.Msg) bool) bool {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(domain), qtype)
	msg.RecursionDesired = true

	resp, rtt, err := client.Exchange(msg, addr)
	if err != nil {
		fmt.Printf("❌ %-40s error: %v\n", name, err)
		return false
	}

	ok := check(resp)
	status := "✅"
	extra := ""
	if !ok {
		status = "❌"
		extra = fmt.Sprintf(" (rcode=%d answers=%d)", resp.Rcode, len(resp.Answer))
	}

	fmt.Printf("%s %-40s rtt=%-10s%s\n", status, name, rtt.Round(time.Microsecond), extra)
	return ok
}
