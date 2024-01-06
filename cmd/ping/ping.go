package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/samlm0/go-ping"
)

var usage = `
Usage:

    ping [-c count] [-i interval] [-t timeout] host

Examples:

    # ping google 5 times
    ping -c 5 www.google.com

    # ping google 5 times at 500ms intervals
    ping -c 5 -i 500ms www.google.com

`

func main() {
	timeout := flag.Duration("t", time.Second, "")
	interval := flag.Duration("i", time.Second, "")
	count := flag.Int("c", 0, "")
	flag.Usage = func() {
		fmt.Print(usage)
	}
	flag.Parse()
	host := flag.Arg(0)
	p, err := ping.New(host)
	if err != nil {
		fmt.Println("Failed to ping target host:", err)
		return
	}
	p.Count = *count
	p.Interval = 1 * *interval
	p.Timeout = 1 * *timeout
	p.OnEvent = func(e *ping.PacketEvent, err error) {
		if e.IsTimeout {
			fmt.Println("Request timeout for icmp_seq " + strconv.Itoa(e.Seq))
			return
		}
		fmt.Printf("%d bytes from %s: icmp_seq=%d ttl=%v time=%.2f ms\n",
			e.Size, e.From, e.Seq, e.TTL, float32(e.Latency.Microseconds())/1000)
	}
	fmt.Printf("PING %s (%s): %d data bytes\n", host, p.Host.IP, p.Size)

	ctx, cancel := context.WithCancel(context.Background())

	// listen ctrl+c signal
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	p.Start(ctx)

	statistic := p.GetStatistic()
	fmt.Printf("\n--- %v ping statistics ---\n", host)
	fmt.Printf("%d packets transmitted, %d received, %.2f%% packet loss, time %v ms\n",
		statistic.SendCount, statistic.ReceivedCount, (1-float32(statistic.ReceivedCount)/float32(statistic.SendCount))*100, statistic.TimeTotal.Milliseconds())
	fmt.Printf("rtt min/avg/max/mdev = %.2f/%.2f/%.2f/%.2f ms\n",
		float32(statistic.TimeMin.Microseconds())/1000,
		float32(statistic.TimeAvg.Microseconds())/1000,
		float32(statistic.TimeMax.Microseconds())/1000,
		float32(statistic.TimeMdev.Microseconds())/1000,
	)
}
