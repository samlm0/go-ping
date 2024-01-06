package ping

import (
	"context"
	"crypto/rand"
	"errors"
	mrand "math/rand"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type Pinger struct {
	Host     *net.IPAddr
	Interval time.Duration
	Timeout  time.Duration
	Count    int
	Size     int
	id       uint16
	OnEvent  func(*PacketEvent, error)

	statistic *PacketStatistic
}

type PacketEvent struct {
	IsTimeout bool
	From      string
	Seq       int
	Size      int
	TTL       int
	Latency   time.Duration
	Message   *icmp.Message
}

type PacketStatistic struct {
	SendCount     int           `json:"count.send_count"`
	ReceivedCount int           `json:"count.received_count"`
	LossedCount   int           `json:"count.lossed_count"`
	TimeTotal     time.Duration `json:"time_total"`
	TimeMax       time.Duration `json:"time_max"`
	TimeMin       time.Duration `json:"time_min"`
	TimeAvg       time.Duration `json:"time_avg"`
	TimeMdev      time.Duration `json:"time_mdev"`
}

func New(target string) (*Pinger, error) {
	addr, err := net.ResolveIPAddr("ip:icmp", target)
	if err != nil {
		return nil, err
	}

	if len(addr.String()) == 0 {
		return nil, errors.New("failed to resolve target host")
	}

	p := &Pinger{
		Host:     addr,
		Interval: 1 * time.Second,
		Timeout:  5 * time.Second,
		Count:    0,
		Size:     56,
		statistic: &PacketStatistic{
			SendCount:     0,
			ReceivedCount: 0,
			LossedCount:   0,
			TimeTotal:     0,
			TimeMax:       0,
			TimeMin:       0,
			TimeAvg:       0,
			TimeMdev:      0,
		},
	}

	p.id = uint16(mrand.Int())
	return p, nil
}

func (p *Pinger) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ticker := time.NewTicker(p.Interval)
	i := 1
	p.sendPacket(i, cancel)

	for {
		select {
		case <-ctx.Done():
			ticker.Stop()
			return
		case <-ticker.C:
			i++
			p.sendPacket(i, cancel)
			if p.Count == 0 {
				continue
			}

			if i+1 > p.Count {
				ticker.Stop()
				return
			}
		}
	}
}

func (p *Pinger) GetStatistic() PacketStatistic {
	return *p.statistic
}

func (p *Pinger) updateStatistic(e *PacketEvent) {
	p.statistic.SendCount++
	if e.IsTimeout {
		p.statistic.LossedCount++
	} else {
		p.statistic.ReceivedCount++
	}
	p.statistic.TimeTotal += e.Latency
	if p.statistic.TimeTotal != 0 && p.statistic.ReceivedCount != 0 {
		p.statistic.TimeAvg = p.statistic.TimeTotal / time.Duration(p.statistic.ReceivedCount)
	}
	if p.statistic.TimeMax < e.Latency {
		p.statistic.TimeMax = e.Latency
	}

	if p.statistic.TimeMin == 0 || p.statistic.TimeMin > e.Latency {
		p.statistic.TimeMin = e.Latency
	}

	p.statistic.TimeMdev = p.statistic.TimeMax - p.statistic.TimeMin
}

func (p *Pinger) sendPacket(seq int, cancel context.CancelFunc) {
	isIPv4 := p.Host.IP.To4() != nil
	// for better maintenance
	if isIPv4 {
		p.sendV4Packet(seq, cancel)
	} else {
		p.sendV6Packet(seq, cancel)
	}
}

func (p *Pinger) sendV4Packet(seq int, cancel context.CancelFunc) {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	// make sure we can get ttl
	conn.IPv4PacketConn().SetControlMessage(ipv4.FlagTTL, true)

	conn.SetWriteDeadline(time.Now().Add(p.Timeout))
	conn.SetReadDeadline(time.Now().Add(p.Timeout))

	if seq > p.Count && p.Count != 0 {
		cancel()
		return
	}

	body := make([]byte, p.Size)

	_, _ = rand.Read(body)

	packet := &icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(p.id),
			Seq:  seq,
			Data: body,
		},
	}

	data, _ := packet.Marshal(nil)

	// count from send packet
	timeA := time.Now()
	length, err := conn.WriteTo(data, p.Host)
	if err != nil {
		if p.OnEvent != nil {
			p.OnEvent(&PacketEvent{Seq: seq, IsTimeout: true}, err)
		}
		p.updateStatistic(&PacketEvent{Seq: seq, IsTimeout: true})
		return
	}
	buf := make([]byte, 1500)

	var ttl int
	n, cm4, addr, err := conn.IPv4PacketConn().ReadFrom(buf)
	if cm4 != nil {
		ttl = cm4.TTL
	}
	// count end from recive packet
	timeB := time.Now()
	if err == nil && addr.String() != p.Host.String() {
		if p.OnEvent != nil {
			p.OnEvent(&PacketEvent{Seq: seq, IsTimeout: true}, err)
		}
		p.updateStatistic(&PacketEvent{Seq: seq, IsTimeout: true})
		return
	}

	event := &PacketEvent{
		Seq:     seq,
		Size:    length,
		Latency: timeB.Sub(timeA),
		TTL:     ttl,
	}
	if err != nil && err.(net.Error).Timeout() {
		event.IsTimeout = true
	} else {
		event.From = addr.String()
	}

	msg, err := icmp.ParseMessage(ipv4.ICMPTypeEcho.Protocol(), buf[:n])
	if err == nil {
		event.Message = msg
	}

	if p.OnEvent != nil {
		p.OnEvent(event, nil)
	}
	p.updateStatistic(event)
}

func (p *Pinger) sendV6Packet(seq int, cancel context.CancelFunc) {
	conn, err := icmp.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	conn.IPv6PacketConn().SetControlMessage(ipv6.FlagHopLimit, true)
	timeA := time.Now()
	conn.SetWriteDeadline(time.Now().Add(p.Timeout))
	conn.SetReadDeadline(time.Now().Add(p.Timeout))

	if seq > p.Count && p.Count != 0 {
		cancel()
		return
	}

	body := make([]byte, p.Size)

	_, _ = rand.Read(body)
	packet := &icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(p.id),
			Seq:  seq,
			Data: body,
		},
	}

	data, _ := packet.Marshal(nil)
	length, err := conn.WriteTo(data, p.Host)
	if err != nil {
		if p.OnEvent != nil {
			p.OnEvent(&PacketEvent{Seq: seq, IsTimeout: true}, err)
		}
		p.updateStatistic(&PacketEvent{Seq: seq, IsTimeout: true})
		return
	}
	buf := make([]byte, 1500)
	var ttl int
	n, cm6, addr, err := conn.IPv6PacketConn().ReadFrom(buf)
	if cm6 != nil {
		ttl = cm6.HopLimit
	}
	timeB := time.Now()
	if err == nil && addr.String() != p.Host.String() {
		if p.OnEvent != nil {
			p.OnEvent(&PacketEvent{Seq: seq, IsTimeout: true}, err)
		}
		p.updateStatistic(&PacketEvent{Seq: seq, IsTimeout: true})
		return
	}

	event := &PacketEvent{
		Seq:     seq,
		Size:    length,
		Latency: timeB.Sub(timeA),
		TTL:     ttl,
	}
	if err != nil && err.(net.Error).Timeout() {
		event.IsTimeout = true
	} else {
		event.From = addr.String()
	}

	msg, err := icmp.ParseMessage(ipv6.ICMPTypeEchoReply.Protocol(), buf[:n])
	if err == nil {
		event.Message = msg
	}

	if p.OnEvent != nil {
		p.OnEvent(event, nil)
	}
	p.updateStatistic(event)
}
