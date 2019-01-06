package main

import (
	"fmt"

	"github.com/google/gousb"
)

type ant struct {
	cxt  *gousb.Context
	dev  *gousb.Device
	done func()
	iep  *gousb.InEndpoint
	oep  *gousb.OutEndpoint
}

func accessANT() (a *ant, err error) {
	a = &ant{}
	defer func() {
		if err == nil {
			return
		}
		a.Close()
	}()
	a.cxt = gousb.NewContext()

	// Open the Dynastream Innovations, Inc. ANTUSB-m Stick
	dev, err := a.cxt.OpenDeviceWithVIDPID(gousb.ID(0x0fcf), gousb.ID(0x1009))
	if err != nil {
		return nil, err
	}
	a.dev = dev

	if err := dev.Reset(); err != nil {
		return nil, err
	}

	// It turns out there's only one config
	for i, config := range dev.Desc.Configs {
		fmt.Println(i, config.String())
	}

	intf, done, err := dev.DefaultInterface()
	if err != nil {
		return nil, err
	}
	a.done = done

	fmt.Println(intf.String())
	a.oep, err = intf.OutEndpoint(0x01)
	if err != nil {
		return nil, err
	}

	a.iep, err = intf.InEndpoint(0x81)
	if err != nil {
		return nil, err
	}

	return a, err
}

func (a *ant) Close() {
	if a.done != nil {
		a.done()
	}
	if a.dev != nil {
		a.dev.Close()
	}
	if a.cxt != nil {
		a.cxt.Close()
	}
}
