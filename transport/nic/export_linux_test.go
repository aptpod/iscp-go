package nic

func SetBindToDevice(dc *DialContext, f func(fd int, nic string) error) {
	dc.bindToDevice = f
}
