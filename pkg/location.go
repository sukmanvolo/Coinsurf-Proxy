package pkg

type LocationLookup interface {
	FindCountry(country string) bool
	FindRegion(country string, region string) bool
	FindCity(country string, city string) bool
}
