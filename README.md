# Proxy

## Glocary
**Customer** - this type of user can only be used to transfer data. It cannot be used to sell his own data using apps.

**Node** - this type of user can only be used to sell data using apps. It cannot be used to make HTTP requests.

**Data** - data that customer has on his account. The accounting is done in bytes



# F.A.Q

## Proxy

**Port**

**7070** is default port that is used to access the proxy 

**Domain**

**proxy.coinsurf.com** is default domain that is used 

### What targeting options are available?
|Parameter|Possible Values|Description|
|--|--|--|
|country| 2 chars, ISO 3166, use **"rr"** to target a random country |Route request through a specific country|
|region  |30 chars max, no whitespace|Route request through a specific region |
|city  |30 chars max, no whitespace| Route request through a specific city |
|session|30 chars max, no whitespace| Sticky session identifier|
|duration|Number of seconds| Sticky session lifetime|
|ipv4|| Router request only through IPv4 IP's|
|ipv6|| Router request only through IPv6 IP's|

**Examples of username authentication and targeting**

**{{password}}** - is customer's proxy password

Country Targeting
``country-us:{{password}}``

``country-ca:{{password}}``


Random Country Targeting

``country-rr:{{password}}``

Region Targeting

``country-us-region-california:{{password}}``

``country-ca-region-ontario:{{password}}``

``country-ua-region-donetsk:{{password}}``

> **Note:** Country is required to target a specific region


City Targeting

``country-us-region-california-city-los_angeles:{{password}}``

``country-us-city-los_angeles:{{password}}``

``country-ca-city-toronto:{{password}}``

``country-br-city-saopaulo:{{password}}``

``country-ua-donetska-donetsk:{{password}}``

> **Note:** Region is optional when targeting a specific city


Session Targeting

``country-us-session-adkljhASduiashd123:{{password}}``

``country-us-session-123123412:{{password}}``

> **Note:** Session identifier can be any string from 8 to 30 characters long. Sessions can be used with any type of targeting and ipv4/v6 options


Session Duration

``country-us-session-aksdjlasjdha-duration-300:{{password}}``

``country-ca-region-ontario-city-toronto-session-12839217-duration-500:{{password}}``

``country-ua-city-donetsk-session-18237193-duration-600:{{password}}``


IPv4 & IPv6 Targeting

``country-us-ipv4:{{password}}``

``country-ca-ipv6:{{password}}``

``country-ua-session-sessionName123-ipv6:{{password}}``

Full curl example

``curl --proxy "country-rr:{{password}}@proxy.coinsurf.com:7070"  "http://ip-api.com/json/?fields=61439" -v``


## API
### How to create a customer?
Make a sign up call and set the flag is_customer = true.

**Method: POST
URL: /api/v1/signup
Body:**
```json
{
  "username": "data@data.com",
  "email": "data@data.com",
  "password": "data@data.com",
  "is_customer": true
}
```
> **Note:** If is_customer has not been passed, it will be set to false


### How to manage customer's data?
Make a user edit call and assign data you want user to have.

**Method: PATCH
URL: /api/v1/users/{{id}}
URL Params: id - uuid user identifier
Body:**
```json
{
  "data": 1000000000
}
```
> **Note:** Data can not be less than 0. **This endpoint is only available to admins**, so make sure to 
> use appropriate token when making this call 

### How to de/activate customer?
Make a user edit call and set is_active = false/true. Inactive customer can not make HTTP requests through the proxy but their data remains on account.

**Method: PATCH
URL: /api/v1/users/{{id}}
URL Params: id - uuid user identifier
Body:**
```json
{
  "is_active": false
}
```
