package xui

type Credentials struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	TwoFactorCode string `json:"twoFactorCode,omitempty"`
}

type Capabilities struct {
	WriteEnabled bool
	Capabilities []string
}

type AdminCapabilities struct {
	ContractVersion string
	CanUpdate       bool
	CanBridge       bool
	TOTPCompatible  bool
}

type BrowserSession struct {
	CookieName string
	Token      []byte
}

type Inbound struct {
	ID       int64  `json:"id"`
	Tag      string `json:"tag"`
	Remark   string `json:"remark"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}

type Outbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

type Snapshot struct {
	Inbounds        []Inbound
	Outbounds       []Outbound
	XraySetting     map[string]any
	OutboundTestURL string
}

type DesiredGroup struct {
	ResourceName                  string
	SOCKSPort                     int
	VLESSPort                     int
	MixedPort                     int
	VLESSClientID                 string
	MixedUsername                 string
	MixedPassword                 string
	MixedSourceRestrictionEnabled bool
	MixedSourceCIDRs              []string
	RealityTarget                 string
	RealityServerName             string
}

type ManagedGroup struct {
	ResourceName    string
	VLESSInboundID  int64
	MixedInboundID  int64
	VLESSInboundTag string
	MixedInboundTag string
	OutboundTag     string
	Fingerprint     string
	PublicKey       string
	ShortID         string
	ServerName      string
	MLDSA65Verify   string
}

type ManagedAggregate struct {
	ResourceName    string
	VLESSInboundID  int64
	VLESSInboundTag string
	VLESSPort       int
	PublicKey       string
	ShortID         string
	ServerName      string
}

type LegacyMainDesired struct {
	VLESSPort                     int
	MixedPort                     int
	SOCKSPort                     int
	MixedUsername                 string
	MixedPassword                 string
	MixedSourceRestrictionEnabled bool
	MixedSourceCIDRs              []string
	RealityTarget                 string
	RealityServerName             string
}

type LegacyMain struct {
	VLESSInboundID int64
	MixedInboundID int64
	VLESSPort      int
	MixedPort      int
	ClientID       string
	PublicKey      string
	ShortID        string
	ServerName     string
	OutboundTag    string
	MLDSA65Verify  string
}

// SubscriptionDesired describes the VLESS inbounds that may be exposed by the
// Gateway-managed subscription client. Inbound IDs are filtered against the
// authenticated 3x-ui snapshot before any write is made.
type SubscriptionDesired struct {
	ClientEmail string
	ClientUUID  string
	InboundIDs  []int64
}

type Subscription struct {
	ResourceName     string
	ClientID         int64
	ClientEmail      string
	ClientUUID       string
	SubscriptionID   string
	InboundIDs       []int64
	SubscriptionPath string
}

type AdapterError struct {
	Code string
}

func (e *AdapterError) Error() string {
	return "3x-ui adapter failed: " + e.Code
}
