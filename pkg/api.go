package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"net"
	"strings"
)

type API struct {
	password []byte
	router   Router
	resolver Resolver
	logger   *zap.Logger
}

func NewAPI(password string, router Router, ipCheck Resolver, logger *zap.Logger) *API {
	return &API{password: []byte(password), router: router, resolver: ipCheck, logger: logger}
}

type response struct {
	Count   int64   `json:"count"`
	Results []*Node `json:"results"`
}

const (
	queryLimit   = "limit"
	queryOffset  = "offset"
	queryCountry = "country"
	queryRegion  = "region"
	queryVersion = "version"
	queryIP      = "ip"
)

func (api *API) Listen(port int) error {
	addr := fmt.Sprintf(":%d", port)

	r := router.New()
	r.GET("/", api.listNodes)
	r.GET("/countries", api.countryStats)
	r.GET("/pools", api.poolStats)
	r.GET("/pools/online", api.listPoolNodes)
	r.GET("/ip", api.ipCheck)

	return fasthttp.ListenAndServe(addr, r.Handler)
}

type ipCheckResponse struct {
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
}

func (api *API) ipCheck(ctx *fasthttp.RequestCtx) {
	ipV := string(ctx.Request.URI().QueryArgs().Peek("ip"))
	ip := net.ParseIP(ipV)
	if ip == nil {
		ctx.Response.SetStatusCode(fasthttp.StatusBadRequest)
		return
	}

	var err error
	var response = &ipCheckResponse{}
	response.Country, response.Region, response.City, err = api.resolver.LookupIP(ipV)
	if err != nil {
		ctx.Response.SetStatusCode(fasthttp.StatusExpectationFailed)
		return
	}

	data, err := json.Marshal(response)
	if err != nil {
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		return
	}

	ctx.Response.SetStatusCode(fasthttp.StatusOK)
	ctx.Response.SetBody(data)
}

func (api *API) listNodes(ctx *fasthttp.RequestCtx) {
	authorization := ctx.Request.Header.Peek(fasthttp.HeaderAuthorization)
	if len(authorization) == 0 || !bytes.EqualFold(authorization, api.password) {
		ctx.Response.SetStatusCode(fasthttp.StatusUnauthorized)
		return
	}

	filters, err := extractFilters(ctx)
	if err != nil {
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	nodes, err := api.router.GetAll(filters...)
	if err != nil {
		api.logger.Error("failed to get all nodes", zap.Error(err))
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	count, err := api.router.Count(filters...)
	if err != nil {
		api.logger.Error("failed to count nodes", zap.Error(err))
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	r := &response{count, nodes}

	data, err := json.Marshal(r)
	if err != nil {
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	ctx.Response.SetStatusCode(fasthttp.StatusOK)
	ctx.Response.SetBody(data)
}

func (api *API) listPoolNodes(ctx *fasthttp.RequestCtx) {
	authorization := ctx.Request.Header.Peek(fasthttp.HeaderAuthorization)
	if len(authorization) == 0 || !bytes.EqualFold(authorization, api.password) {
		ctx.Response.SetStatusCode(fasthttp.StatusUnauthorized)
		return
	}

	nodes, err := api.router.GetPoolNodes()
	if err != nil {
		api.logger.Error("failed to get pool nodes", zap.Error(err))
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	data, err := json.Marshal(nodes)
	if err != nil {
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	ctx.Response.SetStatusCode(fasthttp.StatusOK)
	ctx.Response.SetBody(data)
}

func (api *API) countryStats(ctx *fasthttp.RequestCtx) {
	authorization := ctx.Request.Header.Peek(fasthttp.HeaderAuthorization)
	if len(authorization) == 0 || !bytes.EqualFold(authorization, api.password) {
		ctx.Response.SetStatusCode(fasthttp.StatusUnauthorized)
		return
	}

	countries, err := api.router.GetCountryStats()
	if err != nil {
		api.logger.Error("failed to get country stats", zap.Error(err))
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	data, err := json.Marshal(countries)
	if err != nil {
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	ctx.Response.SetStatusCode(fasthttp.StatusOK)
	ctx.Response.SetBody(data)
}

func (api *API) poolStats(ctx *fasthttp.RequestCtx) {
	authorization := ctx.Request.Header.Peek(fasthttp.HeaderAuthorization)
	if len(authorization) == 0 || !bytes.EqualFold(authorization, api.password) {
		ctx.Response.SetStatusCode(fasthttp.StatusUnauthorized)
		return
	}

	pools, err := api.router.GetPoolStats()
	if err != nil {
		api.logger.Error("failed to get pool stats", zap.Error(err))
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	data, err := json.Marshal(pools)
	if err != nil {
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString(err.Error())
		return
	}

	ctx.Response.SetStatusCode(fasthttp.StatusOK)
	ctx.Response.SetBody(data)
}

func extractFilters(ctx *fasthttp.RequestCtx) ([]Filter, error) {
	filters := make([]Filter, 0)
	limit, err := ctx.Request.URI().QueryArgs().GetUint(queryLimit)
	if err != nil && err != fasthttp.ErrNoArgValue {
		return nil, err
	}

	if limit > 0 {
		filters = append(filters, FilterLimit(limit))
	}

	offset, err := ctx.Request.URI().QueryArgs().GetUint(queryOffset)
	if err != nil && err != fasthttp.ErrNoArgValue {
		return nil, err
	}

	if offset > 0 {
		filters = append(filters, FilterOffset(offset))
	}

	country := ctx.Request.URI().QueryArgs().Peek(queryCountry)
	if len(country) > 0 {
		countries := strings.Split(string(country), ",")
		filters = append(filters, FilterCountry(countries...))
	}

	version := ctx.Request.URI().QueryArgs().Peek(queryVersion)
	if len(version) > 0 {
		versions := strings.Split(string(version), ",")
		filters = append(filters, FilterVersion(versions...))
	}

	ip := ctx.Request.URI().QueryArgs().Peek(queryIP)
	if len(ip) > 0 {
		ips := strings.Split(string(ip), ",")
		filters = append(filters, FilterIP(ips...))
	}

	region := ctx.Request.URI().QueryArgs().Peek(queryRegion)
	if len(region) > 0 {
		regions := strings.Split(string(region), ",")
		filters = append(filters, FilterRegion(regions...))
	}

	return filters, nil
}
