package location

import (
	"github.com/coinsurf-com/proxy/internal/geo"
	"strings"
)

type World struct {
	countries map[string]geo.Country
	regions   map[string]geo.Region
	cities    map[string]geo.City
}

func (b *World) FindCountry(country string) bool {
	if strings.EqualFold(country, countryRandom) {
		return true
	}

	_, ok := b.countries[country]
	return ok
}

func (b *World) FindRegion(country string, region string) bool {
	_, ok := b.regions[country+"-"+region]
	return ok
}

func (b *World) FindCity(country string, city string) bool {
	_, ok := b.cities[country+"-"+city]
	return ok
}

//based on https://github.com/dr5hn/countries-states-cities-database/blob/master/cities.json
func NewWorld(countriesFile string, regionsFile string, citiesFile string) (*World, error) {
	countries, regions, cities, err := geo.Load(countriesFile, regionsFile, citiesFile)
	if err != nil {
		return nil, err
	}

	w := &World{
		countries: countries,
		regions:   regions,
		cities:    cities,
	}

	return w, nil
}
