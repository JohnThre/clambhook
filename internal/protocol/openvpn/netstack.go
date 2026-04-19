package openvpn

// The muxer goroutines that glue the netstack TUN to the data channel.
//
// Flow (client → Internet):
//
//	netstack generates IP packet
//	 → tunToUDP reads it via tunDev.Read
//	 → dataChannel.seal AEAD-encrypts it
//	 → UDP write to the VPN server
//
// Flow (Internet → client):
//
//	UDP read loop (device.go) dispatches P_DATA_V2
//	 → dataChannel.open AEAD-decrypts it
//	 → writeToTUN feeds the netstack
//	 → netstack delivers to whichever Dial/Listen is reading
//
// The udpToTUN direction lives in device.go's udpReadLoop — demux and
// decrypt happen there because the UDP socket is shared with the
// reliable control channel. This file owns only the tunToUDP direction.

// startMuxers launches the tun→udp data path goroutine. udp→tun is
// already running inside udpReadLoop (device.go). Called exactly once,
// after handshake + netstack bring-up have succeeded.
func (i *instance) startMuxers() {
	i.wg.Add(1)
	go i.tunToUDP()
}

// tunToUDP reads raw IP packets from the netstack TUN and ships them out
// the VPN's UDP socket. Uses the batched Read API: gVisor may hand back
// multiple packets in one syscall, which amortises cost on bulk transfer.
func (i *instance) tunToUDP() {
	defer i.wg.Done()

	// Batch capacity of 1 keeps memory modest and ordering trivial; the
	// netstack isn't doing GRO so we almost always get single packets
	// anyway. Size the buffer for a full Ethernet-MTU packet plus slack.
	batchSize := i.tunDev.BatchSize()
	if batchSize < 1 {
		batchSize = 1
	}
	bufs := make([][]byte, batchSize)
	sizes := make([]int, batchSize)
	for j := range bufs {
		bufs[j] = make([]byte, i.mtu+128)
	}

	for {
		select {
		case <-i.ctx.Done():
			return
		default:
		}

		// Offset 0: we don't need headroom for a protocol-specific header
		// because we copy into a fresh buffer in seal() anyway. A future
		// optimisation could seal in-place with headroom reserved.
		n, err := i.tunDev.Read(bufs, sizes, 0)
		if err != nil {
			// Expected on shutdown. If it ever happens live, the daemon
			// should restart the instance — but we don't have that
			// machinery yet.
			return
		}
		for j := 0; j < n; j++ {
			pkt := bufs[j][:sizes[j]]
			ct, err := i.data.seal(pkt)
			if err != nil {
				// Overflow of packet-id counter is the only realistic
				// cause this side; drop and keep going.
				continue
			}
			if _, err := i.udp.Write(ct); err != nil {
				// Transient ENOBUFS will eventually clear; any other
				// error means the UDP socket is dead. Either way,
				// dropping the packet is the right call — the guest's
				// TCP stack will retransmit.
				continue
			}
		}
	}
}
