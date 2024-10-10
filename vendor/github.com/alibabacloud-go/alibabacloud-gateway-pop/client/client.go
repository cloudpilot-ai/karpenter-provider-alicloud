// This file is auto-generated, don't edit it. Thanks.
package client

import (
	spi "github.com/alibabacloud-go/alibabacloud-gateway-spi/client"
	array "github.com/alibabacloud-go/darabonba-array/client"
	encodeutil "github.com/alibabacloud-go/darabonba-encode-util/client"
	map_ "github.com/alibabacloud-go/darabonba-map/client"
	signatureutil "github.com/alibabacloud-go/darabonba-signature-util/client"
	string_ "github.com/alibabacloud-go/darabonba-string/client"
	endpointutil "github.com/alibabacloud-go/endpoint-util/service"
	openapiutil "github.com/alibabacloud-go/openapi-util/service"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
)

type Client struct {
	spi.Client
	Sha256 *string
	Sm3    *string
}

func NewClient() (*Client, error) {
	client := new(Client)
	err := client.Init()
	return client, err
}

func (client *Client) Init() (_err error) {
	_err = client.Client.Init()
	if _err != nil {
		return _err
	}
	client.Sha256 = tea.String("ACS4-HMAC-SHA256")
	client.Sm3 = tea.String("ACS4-HMAC-SM3")
	return nil
}

func (client *Client) ModifyConfiguration(context *spi.InterceptorContext, attributeMap *spi.AttributeMap) (_err error) {
	request := context.Request
	config := context.Configuration
	config.Endpoint, _err = client.GetEndpoint(request.ProductId, config.RegionId, config.EndpointRule, config.Network, config.Suffix, config.EndpointMap, config.Endpoint)
	if _err != nil {
		return _err
	}

	return _err
}

func (client *Client) ModifyRequest(context *spi.InterceptorContext, attributeMap *spi.AttributeMap) (_err error) {
	request := context.Request
	config := context.Configuration
	date := openapiutil.GetTimestamp()
	request.Headers = tea.Merge(map[string]*string{
		"host":                  config.Endpoint,
		"x-acs-version":         request.Version,
		"x-acs-action":          request.Action,
		"user-agent":            request.UserAgent,
		"x-acs-date":            date,
		"x-acs-signature-nonce": util.GetNonce(),
		"accept":                tea.String("application/json"),
	}, request.Headers)
	signatureAlgorithm := util.DefaultString(request.SignatureAlgorithm, client.Sha256)
	hashedRequestPayload := encodeutil.HexEncode(encodeutil.Hash(util.ToBytes(tea.String("")), signatureAlgorithm))
	if !tea.BoolValue(util.IsUnset(request.Stream)) {
		tmp, _err := util.ReadAsBytes(request.Stream)
		if _err != nil {
			return _err
		}

		hashedRequestPayload = encodeutil.HexEncode(encodeutil.Hash(tmp, signatureAlgorithm))
		request.Stream = tea.ToReader(tmp)
		request.Headers["content-type"] = tea.String("application/octet-stream")
	} else {
		if !tea.BoolValue(util.IsUnset(request.Body)) {
			if tea.BoolValue(util.EqualString(request.ReqBodyType, tea.String("json"))) {
				jsonObj := util.ToJSONString(request.Body)
				hashedRequestPayload = encodeutil.HexEncode(encodeutil.Hash(util.ToBytes(jsonObj), signatureAlgorithm))
				request.Stream = tea.ToReader(jsonObj)
				request.Headers["content-type"] = tea.String("application/json; charset=utf-8")
			} else {
				m, _err := util.AssertAsMap(request.Body)
				if _err != nil {
					return _err
				}

				formObj := openapiutil.ToForm(m)
				hashedRequestPayload = encodeutil.HexEncode(encodeutil.Hash(util.ToBytes(formObj), signatureAlgorithm))
				request.Stream = tea.ToReader(formObj)
				request.Headers["content-type"] = tea.String("application/x-www-form-urlencoded")
			}

		}

	}

	if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sm3)) {
		request.Headers["x-acs-content-sm3"] = hashedRequestPayload
	} else {
		request.Headers["x-acs-content-sha256"] = hashedRequestPayload
	}

	if !tea.BoolValue(util.EqualString(request.AuthType, tea.String("Anonymous"))) {
		credential := request.Credential
		if tea.BoolValue(util.IsUnset(credential)) {
			_err = tea.NewSDKError(map[string]interface{}{
				"code":    "ParameterMissing",
				"message": "'config.credential' can not be unset",
			})
			return _err
		}

		authType := credential.GetType()
		if tea.BoolValue(util.EqualString(authType, tea.String("bearer"))) {
			bearerToken := credential.GetBearerToken()
			request.Headers["x-acs-bearer-token"] = bearerToken
			request.Headers["Authorization"] = tea.String("Bearer " + tea.StringValue(bearerToken))
		} else {
			accessKeyId, _err := credential.GetAccessKeyId()
			if _err != nil {
				return _err
			}

			accessKeySecret, _err := credential.GetAccessKeySecret()
			if _err != nil {
				return _err
			}

			securityToken, _err := credential.GetSecurityToken()
			if _err != nil {
				return _err
			}

			if !tea.BoolValue(util.Empty(securityToken)) {
				request.Headers["x-acs-accesskey-id"] = accessKeyId
				request.Headers["x-acs-security-token"] = securityToken
			}

			dateNew := string_.SubString(date, tea.Int(0), tea.Int(10))
			dateNew = string_.Replace(dateNew, tea.String("-"), tea.String(""), nil)
			region := client.GetRegion(request.ProductId, config.Endpoint)
			signingkey, _err := client.GetSigningkey(signatureAlgorithm, accessKeySecret, request.ProductId, region, dateNew)
			if _err != nil {
				return _err
			}

			request.Headers["Authorization"], _err = client.GetAuthorization(request.Pathname, request.Method, request.Query, request.Headers, signatureAlgorithm, hashedRequestPayload, accessKeyId, signingkey, request.ProductId, region, dateNew)
			if _err != nil {
				return _err
			}

		}

	}

	return _err
}

func (client *Client) ModifyResponse(context *spi.InterceptorContext, attributeMap *spi.AttributeMap) (_err error) {
	request := context.Request
	response := context.Response
	if tea.BoolValue(util.Is4xx(response.StatusCode)) || tea.BoolValue(util.Is5xx(response.StatusCode)) {
		_res, _err := util.ReadAsJSON(response.Body)
		if _err != nil {
			return _err
		}

		err, _err := util.AssertAsMap(_res)
		if _err != nil {
			return _err
		}

		requestId := client.DefaultAny(err["RequestId"], err["requestId"])
		if !tea.BoolValue(util.IsUnset(response.Headers["x-acs-request-id"])) {
			requestId = response.Headers["x-acs-request-id"]
		}

		err["statusCode"] = response.StatusCode
		_err = tea.NewSDKError(map[string]interface{}{
			"code":               tea.ToString(client.DefaultAny(err["Code"], err["code"])),
			"message":            "code: " + tea.ToString(tea.IntValue(response.StatusCode)) + ", " + tea.ToString(client.DefaultAny(err["Message"], err["message"])) + " request id: " + tea.ToString(requestId),
			"data":               err,
			"description":        tea.ToString(client.DefaultAny(err["Description"], err["description"])),
			"accessDeniedDetail": client.DefaultAny(err["AccessDeniedDetail"], err["accessDeniedDetail"]),
		})
		return _err
	}

	if tea.BoolValue(util.EqualNumber(response.StatusCode, tea.Int(204))) {
		_, _err = util.ReadAsString(response.Body)
		if _err != nil {
			return _err
		}
	} else if tea.BoolValue(util.EqualString(request.BodyType, tea.String("binary"))) {
		response.DeserializedBody = response.Body
	} else if tea.BoolValue(util.EqualString(request.BodyType, tea.String("byte"))) {
		byt, _err := util.ReadAsBytes(response.Body)
		if _err != nil {
			return _err
		}

		response.DeserializedBody = byt
	} else if tea.BoolValue(util.EqualString(request.BodyType, tea.String("string"))) {
		str, _err := util.ReadAsString(response.Body)
		if _err != nil {
			return _err
		}

		response.DeserializedBody = str
	} else if tea.BoolValue(util.EqualString(request.BodyType, tea.String("json"))) {
		obj, _err := util.ReadAsJSON(response.Body)
		if _err != nil {
			return _err
		}

		res, _err := util.AssertAsMap(obj)
		if _err != nil {
			return _err
		}

		response.DeserializedBody = res
	} else if tea.BoolValue(util.EqualString(request.BodyType, tea.String("array"))) {
		arr, _err := util.ReadAsJSON(response.Body)
		if _err != nil {
			return _err
		}

		response.DeserializedBody = arr
	} else {
		response.DeserializedBody, _err = util.ReadAsString(response.Body)
		if _err != nil {
			return _err
		}

	}

	return _err
}

func (client *Client) GetEndpoint(productId *string, regionId *string, endpointRule *string, network *string, suffix *string, endpointMap map[string]*string, endpoint *string) (_result *string, _err error) {
	if !tea.BoolValue(util.Empty(endpoint)) {
		_result = endpoint
		return _result, _err
	}

	if !tea.BoolValue(util.IsUnset(endpointMap)) && !tea.BoolValue(util.Empty(endpointMap[tea.StringValue(regionId)])) {
		_result = endpointMap[tea.StringValue(regionId)]
		return _result, _err
	}

	_body, _err := endpointutil.GetEndpointRules(productId, regionId, endpointRule, network, suffix)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

func (client *Client) DefaultAny(inputValue interface{}, defaultValue interface{}) (_result interface{}) {
	if tea.BoolValue(util.IsUnset(inputValue)) {
		_result = defaultValue
		return _result
	}

	_result = inputValue
	return _result
}

func (client *Client) GetAuthorization(pathname *string, method *string, query map[string]*string, headers map[string]*string, signatureAlgorithm *string, payload *string, ak *string, signingkey []byte, product *string, region *string, date *string) (_result *string, _err error) {
	signature, _err := client.GetSignature(pathname, method, query, headers, signatureAlgorithm, payload, signingkey)
	if _err != nil {
		return _result, _err
	}

	signedHeaders, _err := client.GetSignedHeaders(headers)
	if _err != nil {
		return _result, _err
	}

	signedHeadersStr := array.Join(signedHeaders, tea.String(";"))
	_result = tea.String(tea.StringValue(signatureAlgorithm) + " Credential=" + tea.StringValue(ak) + "/" + tea.StringValue(date) + "/" + tea.StringValue(region) + "/" + tea.StringValue(product) + "/aliyun_v4_request,SignedHeaders=" + tea.StringValue(signedHeadersStr) + ",Signature=" + tea.StringValue(signature))
	return _result, _err
}

func (client *Client) GetSignature(pathname *string, method *string, query map[string]*string, headers map[string]*string, signatureAlgorithm *string, payload *string, signingkey []byte) (_result *string, _err error) {
	canonicalURI := tea.String("/")
	if !tea.BoolValue(util.Empty(pathname)) {
		canonicalURI = pathname
	}

	stringToSign := tea.String("")
	canonicalizedResource, _err := client.BuildCanonicalizedResource(query)
	if _err != nil {
		return _result, _err
	}

	canonicalizedHeaders, _err := client.BuildCanonicalizedHeaders(headers)
	if _err != nil {
		return _result, _err
	}

	signedHeaders, _err := client.GetSignedHeaders(headers)
	if _err != nil {
		return _result, _err
	}

	signedHeadersStr := array.Join(signedHeaders, tea.String(";"))
	stringToSign = tea.String(tea.StringValue(method) + "\n" + tea.StringValue(canonicalURI) + "\n" + tea.StringValue(canonicalizedResource) + "\n" + tea.StringValue(canonicalizedHeaders) + "\n" + tea.StringValue(signedHeadersStr) + "\n" + tea.StringValue(payload))
	hex := encodeutil.HexEncode(encodeutil.Hash(util.ToBytes(stringToSign), signatureAlgorithm))
	stringToSign = tea.String(tea.StringValue(signatureAlgorithm) + "\n" + tea.StringValue(hex))
	signature := util.ToBytes(tea.String(""))
	if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sha256)) {
		signature = signatureutil.HmacSHA256SignByBytes(stringToSign, signingkey)
	} else if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sm3)) {
		signature = signatureutil.HmacSM3SignByBytes(stringToSign, signingkey)
	}

	_body := encodeutil.HexEncode(signature)
	_result = _body
	return _result, _err
}

func (client *Client) GetSigningkey(signatureAlgorithm *string, secret *string, product *string, region *string, date *string) (_result []byte, _err error) {
	sc1 := tea.String("aliyun_v4" + tea.StringValue(secret))
	sc2 := util.ToBytes(tea.String(""))
	if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sha256)) {
		sc2 = signatureutil.HmacSHA256Sign(date, sc1)
	} else if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sm3)) {
		sc2 = signatureutil.HmacSM3Sign(date, sc1)
	}

	sc3 := util.ToBytes(tea.String(""))
	if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sha256)) {
		sc3 = signatureutil.HmacSHA256SignByBytes(region, sc2)
	} else if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sm3)) {
		sc3 = signatureutil.HmacSM3SignByBytes(region, sc2)
	}

	sc4 := util.ToBytes(tea.String(""))
	if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sha256)) {
		sc4 = signatureutil.HmacSHA256SignByBytes(product, sc3)
	} else if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sm3)) {
		sc4 = signatureutil.HmacSM3SignByBytes(product, sc3)
	}

	hmac := util.ToBytes(tea.String(""))
	if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sha256)) {
		hmac = signatureutil.HmacSHA256SignByBytes(tea.String("aliyun_v4_request"), sc4)
	} else if tea.BoolValue(util.EqualString(signatureAlgorithm, client.Sm3)) {
		hmac = signatureutil.HmacSM3SignByBytes(tea.String("aliyun_v4_request"), sc4)
	}

	_result = hmac
	return _result, _err
}

func (client *Client) GetRegion(product *string, endpoint *string) (_result *string) {
	region := tea.String("center")
	if tea.BoolValue(util.Empty(product)) || tea.BoolValue(util.Empty(endpoint)) {
		_result = region
		return _result
	}

	preRegion := string_.Replace(endpoint, tea.String(".aliyuncs.com"), tea.String(""), nil)
	nodes := string_.Split(preRegion, tea.String("."), nil)
	if tea.BoolValue(util.EqualNumber(array.Size(nodes), tea.Int(2))) {
		region = nodes[1]
	}

	_result = region
	return _result
}

func (client *Client) BuildCanonicalizedResource(query map[string]*string) (_result *string, _err error) {
	canonicalizedResource := tea.String("")
	if !tea.BoolValue(util.IsUnset(query)) {
		queryArray := map_.KeySet(query)
		sortedQueryArray := array.AscSort(queryArray)
		separator := tea.String("")
		for _, key := range sortedQueryArray {
			canonicalizedResource = tea.String(tea.StringValue(canonicalizedResource) + tea.StringValue(separator) + tea.StringValue(encodeutil.PercentEncode(key)))
			if !tea.BoolValue(util.Empty(query[tea.StringValue(key)])) {
				canonicalizedResource = tea.String(tea.StringValue(canonicalizedResource) + "=" + tea.StringValue(encodeutil.PercentEncode(query[tea.StringValue(key)])))
			}

			separator = tea.String("&")
		}
	}

	_result = canonicalizedResource
	return _result, _err
}

func (client *Client) BuildCanonicalizedHeaders(headers map[string]*string) (_result *string, _err error) {
	canonicalizedHeaders := tea.String("")
	sortedHeaders, _err := client.GetSignedHeaders(headers)
	if _err != nil {
		return _result, _err
	}

	for _, header := range sortedHeaders {
		canonicalizedHeaders = tea.String(tea.StringValue(canonicalizedHeaders) + tea.StringValue(header) + ":" + tea.StringValue(string_.Trim(headers[tea.StringValue(header)])) + "\n")
	}
	_result = canonicalizedHeaders
	return _result, _err
}

func (client *Client) GetSignedHeaders(headers map[string]*string) (_result []*string, _err error) {
	headersArray := map_.KeySet(headers)
	sortedHeadersArray := array.AscSort(headersArray)
	tmp := tea.String("")
	separator := tea.String("")
	for _, key := range sortedHeadersArray {
		lowerKey := string_.ToLower(key)
		if tea.BoolValue(string_.HasPrefix(lowerKey, tea.String("x-acs-"))) || tea.BoolValue(string_.Equals(lowerKey, tea.String("host"))) || tea.BoolValue(string_.Equals(lowerKey, tea.String("content-type"))) {
			if !tea.BoolValue(string_.Contains(tmp, lowerKey)) {
				tmp = tea.String(tea.StringValue(tmp) + tea.StringValue(separator) + tea.StringValue(lowerKey))
				separator = tea.String(";")
			}

		}

	}
	_result = make([]*string, 0)
	_body := string_.Split(tmp, tea.String(";"), nil)
	_result = _body
	return _result, _err
}
