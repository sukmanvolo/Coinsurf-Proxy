package keys

//UserKey - returns user id which corresponds to record in Redis
func UserKey(apiKey []byte) string {
	return ":1:" + string(apiKey)
}

func PermissionKey(key []byte) string {
	return string(key) + "permissions"
}
