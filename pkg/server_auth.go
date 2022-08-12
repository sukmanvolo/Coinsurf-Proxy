package pkg

import _ "github.com/mailru/easyjson/gen"

//easyjson:json
type authMessage struct {
	APIKey    string `json:"api_key"`
	UserAgent string `json:"user_agent"`
}
