package xapi

import "fmt"

type Profile uint8

const (
	XPlatformXML4000 Profile = iota
	NexacroXML4000
	NexacroJSON10
	NexacroSSV
	XPlatformSSV
	NexacroBinary5000
	XPlatformBinary5000
)

func (p Profile) String() string {
	switch p {
	case XPlatformXML4000:
		return "xplatform-xml-4000"
	case NexacroXML4000:
		return "nexacro-xml-4000"
	case NexacroJSON10:
		return "nexacro-json-1.0"
	case NexacroSSV:
		return "nexacro-ssv"
	case XPlatformSSV:
		return "xplatform-ssv"
	case NexacroBinary5000:
		return "nexacro-binary-5000"
	case XPlatformBinary5000:
		return "xplatform-binary-5000"
	default:
		return "unknown"
	}
}
func (p Profile) json() bool { return p == NexacroJSON10 }
func (p Profile) ssv() bool {
	return p == NexacroSSV || p == XPlatformSSV
}
func (p Profile) binary() bool {
	return p == NexacroBinary5000 || p == XPlatformBinary5000
}
func (p Profile) namespace() string {
	if p == XPlatformXML4000 {
		return "http://www.tobesoft.com/platform/Dataset"
	}
	return "http://www.nexacroplatform.com/platform/dataset"
}
func (p Profile) valid() error {
	if p > XPlatformBinary5000 {
		return fmt.Errorf("xapi: unsupported profile %d", p)
	}
	return nil
}
