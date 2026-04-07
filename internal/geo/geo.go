package geo

// Location represents a server's geographic location.
type Location struct {
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	City        string  `json:"city"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

// Lookup resolves the geolocation for an IP address or hostname.
func Lookup(address string) (*Location, error) {
	// TODO: implement geolocation lookup
	return &Location{}, nil
}
