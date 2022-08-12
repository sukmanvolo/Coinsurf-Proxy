package geo

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"strings"
	"sync"
)

type Country struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Region struct {
	CountryCode string `json:"country_code"`

	Code string `json:"code"`
	Name string `json:"name"`
}

type City struct {
	RegionCode  string `json:"region_code"`
	CountryCode string `json:"country_code"`

	Name string `json:"name"`
}

type record struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	StateID     int    `json:"state_id"`
	StateCode   string `json:"state_code"`
	StateName   string `json:"state_name"`
	CountryID   int    `json:"country_id"`
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name"`
}

func rename(n string) string {
	n = strings.Replace(n, "-", "_", -1)
	n = strings.Replace(n, " ", "_", -1)
	n = strings.Replace(n, "`", "", -1)
	n = strings.Replace(n, "’", "", -1)
	n = strings.Replace(n, "'", "", -1)
	n = strings.Replace(n, ",", "", -1)

	return strings.ToLower(n)
}

func removeRegionWord(n string) string {
	n = strings.Replace(n, "_oblast", "", -1)
	n = strings.Replace(n, "_state", "", -1)
	n = strings.Replace(n, "_region", "", -1)
	n = strings.Replace(n, "_province", "", -1)
	n = strings.Replace(n, "_division", "", -1)
	n = strings.Replace(n, "_county", "", -1)
	n = strings.Replace(n, "_prefecture", "", -1)
	n = strings.Replace(n, "_district", "", -1)
	n = strings.Replace(n, "_governorate", "", -1)
	n = strings.Replace(n, "_municipality", "", -1)
	n = strings.Replace(n, "_voivodeship", "", -1)
	n = strings.Replace(n, "_krai", "", -1)
	n = strings.Replace(n, "_pradesh", "", -1)
	n = strings.Replace(n, "_quarter", "", -1)

	return n
}

//based on https://github.com/dr5hn/countries-states-cities-database/blob/master/cities.json
func Format(filePath string) ([]Country, []Region, []City, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, nil, err
	}
	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, nil, nil, err
	}

	var records []record
	err = json.Unmarshal(data, &records)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(records) < 10 {
		return nil, nil, nil, errors.New("invalid cities_initial.json was given")
	}

	var countriesTemp = make(map[int]Country)
	var regionsTemp = make(map[int]Region)
	var citiesTemp = make(map[int]City)

	for _, r := range records {
		cc := strings.ToLower(r.CountryCode)
		sc := strings.ToLower(r.StateCode)

		_, ok := countriesTemp[r.CountryID]
		if !ok {
			countriesTemp[r.CountryID] = Country{
				Code: cc,
				Name: rename(r.CountryName),
			}
		}

		_, ok = regionsTemp[r.StateID]
		if !ok {
			regionsTemp[r.StateID] = Region{
				CountryCode: cc,
				Code:        sc,
				Name:        removeRegionWord(rename(r.StateName)),
			}
		}

		_, ok = citiesTemp[r.ID]
		if !ok {
			citiesTemp[r.ID] = City{
				RegionCode:  sc,
				CountryCode: cc,
				Name:        rename(r.Name),
			}
		}
	}

	var countries = make([]Country, len(countriesTemp))
	var regions = make([]Region, len(regionsTemp))
	var cities = make([]City, len(citiesTemp))

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		var i int
		for _, c := range countriesTemp {
			countries[i] = c
			i++
		}
	}()

	go func() {
		defer wg.Done()
		var i int
		for _, r := range regionsTemp {
			regions[i] = r
			i++
		}
	}()

	go func() {
		defer wg.Done()
		var i int
		for _, c := range citiesTemp {
			cities[i] = c
			i++
		}
	}()

	wg.Wait()

	return countries, regions, cities, nil
}

func SaveJSON(folder string, countries []Country, regions []Region, cities []City) error {
	countriesF, err := os.Create(folder + "countries.json")
	if err != nil {
		return err
	}
	defer countriesF.Close()

	data, err := json.Marshal(countries)
	if err != nil {
		return err
	}

	_, err = countriesF.Write(data)
	if err != nil {
		return err
	}

	regionsF, err := os.Create(folder + "regions.json")
	if err != nil {
		return err
	}
	defer regionsF.Close()

	data, err = json.Marshal(regions)
	if err != nil {
		return err
	}

	_, err = regionsF.Write(data)
	if err != nil {
		return err
	}

	citiesF, err := os.Create(folder + "cities.json")
	if err != nil {
		return err
	}
	defer citiesF.Close()

	data, err = json.Marshal(cities)
	if err != nil {
		return err
	}

	_, err = citiesF.Write(data)
	if err != nil {
		return err
	}

	return nil
}

func Load(countriesFile string, regionsFile string, citiesFile string) (map[string]Country, map[string]Region, map[string]City, error) {
	data, err := ioutil.ReadFile(countriesFile)
	if err != nil {
		return nil, nil, nil, err
	}

	var countries []Country
	err = json.Unmarshal(data, &countries)
	if err != nil {
		return nil, nil, nil, err
	}

	data, err = ioutil.ReadFile(regionsFile)
	if err != nil {
		return nil, nil, nil, err
	}

	var regions []Region
	err = json.Unmarshal(data, &regions)
	if err != nil {
		return nil, nil, nil, err
	}

	data, err = ioutil.ReadFile(citiesFile)
	if err != nil {
		return nil, nil, nil, err
	}

	var cities []City
	err = json.Unmarshal(data, &cities)
	if err != nil {
		return nil, nil, nil, err
	}

	var co = make(map[string]Country)
	for _, r := range countries {
		co[r.Code] = r
	}

	var rr = make(map[string]Region)
	for _, r := range regions {
		rr[r.CountryCode+"-"+r.Name] = r
	}

	var cc = make(map[string]City)
	for _, r := range cities {
		cc[r.CountryCode+"-"+r.Name] = r
	}

	return co, rr, cc, nil
}
