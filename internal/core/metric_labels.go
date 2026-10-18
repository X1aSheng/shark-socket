package core

// ProtocolLabel returns the pre-built ("protocol", name) label pair for p.
//
// Every protocol-scoped metric carries the same label, and the label set is a
// closed enum: building a []string per observation would allocate on the
// message path, so the pairs are built once. Callers must not mutate the
// returned slice.
//
// An out-of-enum protocol (a caller-defined string) gets a freshly built pair.
// That case does not appear on a hot path; correctness matters more than
// avoiding one allocation there.
func ProtocolLabel(p Protocol) []string {
	switch p {
	case ProtocolTCP:
		return protocolLabelTCP
	case ProtocolUDP:
		return protocolLabelUDP
	case ProtocolHTTP:
		return protocolLabelHTTP
	case ProtocolWS:
		return protocolLabelWS
	case ProtocolCoAP:
		return protocolLabelCoAP
	case ProtocolQUIC:
		return protocolLabelQUIC
	case ProtocolGRPCWeb:
		return protocolLabelGRPCWeb
	case ProtocolCustom:
		return protocolLabelCustom
	default:
		return []string{"protocol", string(p)}
	}
}

var (
	protocolLabelTCP     = []string{"protocol", string(ProtocolTCP)}
	protocolLabelUDP     = []string{"protocol", string(ProtocolUDP)}
	protocolLabelHTTP    = []string{"protocol", string(ProtocolHTTP)}
	protocolLabelWS      = []string{"protocol", string(ProtocolWS)}
	protocolLabelCoAP    = []string{"protocol", string(ProtocolCoAP)}
	protocolLabelQUIC    = []string{"protocol", string(ProtocolQUIC)}
	protocolLabelGRPCWeb = []string{"protocol", string(ProtocolGRPCWeb)}
	protocolLabelCustom  = []string{"protocol", string(ProtocolCustom)}
)
