package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/google/gousb"
	"github.com/pkg/errors"
)

func main() {
	var deviceNumber uint
	flag.UintVar(&deviceNumber, "device", 0, "Specify the device number to get a particular HRM")
	flag.Parse()

	sensorID, err := ensureSensor()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("sensorID=%s\n", sensorID)

	// So, we've opened the ANT USB stick, and grabbed in & out interfaces to it.
	a, err := accessANT()
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	if err := configureAndOpenChannel(a, &heartrate, uint16(deviceNumber)); err != nil {
		log.Fatal(err)
	}

	cxt, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		cancel()
	}()

	defer func() {
		// close channel
		_, err = sendAndWait(a,
			0x01, // length
			0x4C, // close channel
			1,    // channel number
		)
		if err != nil {
			log.Fatalf("Failed set channel ID. %s", err)
		}

	}()

	buf := make([]byte, 56)
	var fpi packetInfo
	for i := 0; ; i++ {
		// Read the next USB packet
		n, err := a.iep.ReadContext(cxt, buf)
		if err != nil {
			fmt.Println()
			log.Fatal(err)
		}

		pi, err := parsePacket(buf[:n])
		if err != nil {
			if err, ok := err.(*errorResponse); ok {
				if err.code == 2 {
					continue
				}
			}
			log.Fatal(err)
		}

		if pi.hr != 0 {
			if pi.hr != fpi.hr {
				if err := notifyHR(sensorID, pi.hr); err != nil {
					log.Printf("failed to notify HR. %s", err)
				}
			}
			fpi.hr = pi.hr
		}
		if pi.deviceNumber != 0 {
			fpi.deviceNumber = pi.deviceNumber
		}
		if pi.manuID != 0 {
			fpi.manuID = pi.manuID
		}
		if pi.serial != 0 {
			fpi.serial = pi.serial
		}

		fmt.Printf("\u001b[1000D%x %d-%d %d %c", fpi.manuID, fpi.serial, fpi.deviceNumber, fpi.hr, progress[i%len(progress)])
	}
}

func configureAndOpenChannel(a *ant, dt *antDeviceType, deviceNumber uint16) error {
	var err error
	_, err = sendAndWait(a,
		0x01, // length
		0x4A, // system reset
		0x00, // network number
	)
	if err != nil {
		return errors.Wrap(err, "failed system reset")
	}
	// Wait 500ms after reset
	time.Sleep(500 * time.Millisecond)

	// Network key is a "magic key" to let you participate in ANT+. Should be obtained from the ANT+ org
	_, err = sendAndWait(a,
		0x09, // length
		0x46, // set network key
		0x01, // network number
		0xB9, 0xA5, 0x21, 0xFB, 0xBD, 0x72, 0xC3, 0x45,
	)
	if err != nil {
		return errors.Wrap(err, "failed to write network key")
	}

	// Seems we basically discover HR monitors by trying to connect to one. If we have the device number of
	// one we can specify that and that's likely what we'll get

	// assign channel
	_, err = sendAndWait(a,
		0x03, // length
		0x42, // assign channel
		1,    // channel number
		dt.channelType,
		1, // network number
	)
	if err != nil {
		errors.Wrap(err, "failed assign channel")
	}

	// set channel RF frequency
	_, err = sendAndWait(a,
		0x02, // length
		0x45, // set channel RF
		1,    // channel number
		dt.rfChannel,
	)
	if err != nil {
		return errors.Wrap(err, "failed set channel RF")
	}

	// set channel ID
	_, err = sendAndWait(a,
		0x05, // length
		0x51, // set channel ID
		1,    // channel number
		byte(deviceNumber&0xFF),
		byte((deviceNumber&0xFF00)>>8),
		dt.deviceType,
		dt.transmissionType,
	)
	if err != nil {
		errors.Wrap(err, "failed set channel ID")
	}

	// set channel period
	_, err = sendAndWait(a,
		0x03, // length
		0x43, // set channel period
		1,    // channel number
		byte(dt.channelPeriod&0xFF),
		byte((dt.channelPeriod&0xFF00)>>8),
	)
	if err != nil {
		errors.Wrap(err, "failed set channel ID")
	}

	// set channel search timeout
	_, err = sendAndWait(a,
		0x02, // length
		0x44, // set channel search timeout
		1,    // channel number
		dt.searchTimeout,
	)
	if err != nil {
		errors.Wrap(err, "failed set search timeout")
	}

	// Enable extended messages, so we receive data on the HR monitor in each message
	_, err = sendAndWait(a,
		0x02, // length
		0x6E, // libconfig
		0,    // filler
		0xE0,
	)
	if err != nil {
		errors.Wrap(err, "failed extended messages")
	}

	// open channel
	_, err = sendAndWait(a,
		0x01, // length
		0x4B, // open channel
		1,    // channel number
	)
	if err != nil {
		errors.Wrap(err, "failed open channel")
	}

	return nil
}

var progress = []byte{'|', '/', '-', '\\'}

type antDeviceType struct {
	channelType      byte
	rfChannel        byte
	transmissionType byte
	deviceType       byte
	deviceNumber     uint16
	channelPeriod    uint16
	searchTimeout    byte
}

var heartrate = antDeviceType{
	channelType:      0x00,
	rfChannel:        57,
	transmissionType: 0,
	deviceType:       120,
	deviceNumber:     0,
	channelPeriod:    8070, // 32768/rate - so 4 per second
	searchTimeout:    12,   // Search timeout 30 seconds
}

func sendAndWait(a *ant, data ...byte) (packetInfo, error) {
	return sendAndWaitCxt(context.Background(), a, data...)
}

func sendAndWaitCxt(cxt context.Context, a *ant, data ...byte) (packetInfo, error) {
	if err := send(a.oep, data...); err != nil {
		return packetInfo{}, err
	}

	buf := make([]byte, 56)
	n, err := a.iep.ReadContext(cxt, buf)
	if err != nil {
		return packetInfo{}, err
	}
	// parse this and make sure it's an OK response
	return parsePacket(buf[:n])
}

// send sends a packet
// Packet is
// sync = A4
// len (of data)
// id
// data
// checksum (XOR of all previous bytes)
func send(ep *gousb.OutEndpoint, data ...byte) error {
	packet := make([]byte, len(data)+2)
	packet[0] = 0xA4
	copy(packet[1:], data)
	var sum byte
	for _, x := range packet {
		sum ^= x
	}
	packet[len(packet)-1] = sum

	_, err := ep.Write(packet)
	return err
}

type packetInfo struct {
	hr           byte
	deviceNumber uint16
	errorCode    byte
	manuID       byte
	serial       uint16
}

type errorResponse struct {
	channelID byte
	msgID     byte
	code      byte
}

func (e errorResponse) Error() string {
	return fmt.Sprintf("received error response to msg %X on channel %x. %d", e.msgID, e.channelID, e.code)
}

func parsePacket(pkt []byte) (pi packetInfo, err error) {

	// sync = A4
	// len (of data)
	// id
	// data
	// checksum
	if len(pkt) < 4 || len(pkt) > 56 {
		return pi, fmt.Errorf("invalid packet length %d", len(pkt))
	}

	var xor byte
	for _, c := range pkt {
		xor ^= c
	}
	if xor != 0 {
		return pi, fmt.Errorf("invalid checksum")
	}

	if pkt[0] != 0xA4 {
		return pi, fmt.Errorf("invalid sync byte %x", pkt[0])
	}

	// fmt.Println(pkt)

	ll := pkt[1]
	if len(pkt) != int(ll)+4 {
		return pi, fmt.Errorf("unexpected packet length %d - should be %d", len(pkt), ll+4)
	}

	// TODO actually return some information!
	// TODO pick out the right message
	switch pkt[2] {
	case 0x40:
		if ll != 3 {
			return pi, fmt.Errorf("Incorrect length")
		}
		// channel ID
		// msg ID
		// code
		pi.errorCode = pkt[5]
		if pi.errorCode != 0 {
			return pi, &errorResponse{
				channelID: pkt[3],
				msgID:     pkt[4],
				code:      pkt[5],
			}
		}
	case 0x4E: // Broadcast data
		if ll < 9 {
			return pi, fmt.Errorf("Incorrect broadcast data length")
		}
		// channel number
		// payload (8 bytes)
		pageNo := pkt[4] & 0x7F
		switch pageNo {
		case 2:
			pi.manuID = pkt[4]
			pi.serial = uint16(pkt[5]) | (uint16(pkt[6]) << 8)
		}
		pi.hr = pkt[11]

		if ll == 20 {
			// These offsets look wrong
			// flags := pkt[12]
			// channelNumber := pkt[13]

			// This order does not match the spec!
			deviceNumber := uint16(pkt[15]) | (uint16(pkt[14]) << 8)
			// deviceType := pkt[16]
			// transType := pkt[17]

			pi.deviceNumber = deviceNumber

			// measType := pkt[18]
			// rssi := pkt[19]
			// thresh := pkt[20]
			// timestamp := int(pkt[21]) | (int(pkt[22]) << 8)
			// fmt.Printf("measure=%x, rssi=%d, thresh=%x, ts=%d\n", measType, rssi, thresh, timestamp)
		}

	case 0x6F: // start-up message
	default:
		fmt.Println(pkt)
	}

	return pi, nil
}
