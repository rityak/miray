package dns

import (
	"net/netip"

	"github.com/miekg/dns"
)

func setEdns0Subnet(message *dns.Msg, clientSubnet netip.Prefix, override bool) bool {
	// the message's own OPT: a scan that kept the last record looked at lost it
	// behind any later record and added a second OPT, a FORMERR (RFC 6891 §6.1.1, T-156)
	optRecord := message.IsEdns0()
	var subnetOption *dns.EDNS0_SUBNET
	if optRecord != nil {
		for _, option := range optRecord.Option {
			if subnet, isEDNS0Subnet := option.(*dns.EDNS0_SUBNET); isEDNS0Subnet {
				if !override {
					return false
				}
				subnetOption = subnet
				break
			}
		}
	} else {
		message.SetEdns0(edns0UDPSize, false) // a size of 0 would mean 512
		optRecord = message.IsEdns0()
	}
	if subnetOption == nil {
		subnetOption = new(dns.EDNS0_SUBNET)
		optRecord.Option = append(optRecord.Option, subnetOption)
	}
	subnetOption.Code = dns.EDNS0SUBNET
	if clientSubnet.Addr().Is4() {
		subnetOption.Family = 1
	} else {
		subnetOption.Family = 2
	}
	subnetOption.SourceNetmask = uint8(clientSubnet.Bits())
	subnetOption.Address = clientSubnet.Addr().AsSlice()
	return true
}
