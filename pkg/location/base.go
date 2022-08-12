package location

import (
	"encoding/csv"
	"github.com/pariz/gountries"
	"io"
	"os"
	"strings"
)

var (
	countryRandom = "rr"
	countryGB     = "gb"
	countryUK     = "uk"
)

type Base struct {
	cities map[string]struct{}
	query  *gountries.Query
}

func (b *Base) FindCountry(country string) bool {
	if strings.EqualFold(country, countryRandom) {
		return true
	}

	_, err := b.query.FindCountryByAlpha(country)
	if err != nil && strings.EqualFold(country, countryUK) {
		country = countryGB
		_, err = b.query.FindCountryByAlpha(country)
		if err != nil {
			return false
		}
	}

	return true
}

func (b *Base) FindRegion(country string, region string) bool {
	c, err := b.query.FindCountryByAlpha(country)
	if err != nil && strings.EqualFold(country, countryUK) {
		country = countryGB
		c, err = b.query.FindCountryByAlpha(country)
		if err != nil {
			return false
		}
	}

	_, err = c.FindSubdivisionByCode(region)
	if err != nil {
		_, err = c.FindSubdivisionByName(region)
	}

	if err != nil {
		return false
	}

	return true
}

func (b *Base) FindCity(country string, city string) bool {
	_, ok := b.cities[country+"-"+city]
	return ok
}

func NewBase(geoFile string) (*Base, error) {
	base := &Base{
		query:  gountries.New(),
		cities: make(map[string]struct{}),
	}

	result, err := os.Open(geoFile)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	r := csv.NewReader(result)
	_, err = r.Read()
	if err != nil {
		return nil, err
	}

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		city := strings.Join(record, "-")
		base.cities[city] = struct{}{}
	}

	return base, nil
}
