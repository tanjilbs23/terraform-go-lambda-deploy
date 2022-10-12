package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
)

type Event struct {
	Arguments struct {
		Input map[string]string `json:"input"`
	}
	Identity struct {
		Claims struct {
			Sub                 string `json:"sub"`
			EmailVerified       bool   `json:"email_verified"`
			Iss                 string `json:"iss"`
			PhoneNumberVerified bool   `json:"phone_number_verified"`
			CognitoUsername     string `json:"cognito:username"`
			OriginJti           string `json:"origin_jti"`
			Aud                 string `json:"aud"`
			EventId             string `json:"event_id"`
			TokenUse            string `json:"token_use"`
			AuthTime            int    `json:"auth_time"`
			PhoneNumber         string `json:"phone_number"`
			Exp                 int    `json:"exp"`
			Iat                 int    `json:"iat"`
			Jti                 string `json:"jti"`
			Email               string `json:"email"`
			Name                string `json:"name"`
		}
		DefaultAuthStrategy string   `json:"defaultAuthStrategy"`
		Groups              string   `json:"groups"`
		Issuer              string   `json:"issuer"`
		SourceIp            []string `json:"sourceIp"`
		Sub                 string   `json:"sub"`
		Username            string   `json:"username"`
	}
	Info struct {
		FieldName           string            `json:"fieldName"`
		ParentTypeName      string            `json:"parentTypeName"`
		SelectionSetGraphQL string            `json:"selectionSetGraphQL"`
		SelectionSetList    []string          `json:"selectionSetList"`
		Variables           map[string]string `json:"variables"`
	}
	Prev    string `json:"prev"`
	Request struct {
		Headers struct {
			Accept                    string `json:"accept"`
			AcceptEncoding            string `json:"accept-encoding"`
			AcceptLanguage            string `json:"accept-language"`
			Authorization             string `json:"authorization"`
			CloudfrontForwardedProto  string `json:"cloudfront-forwarded-proto"`
			CloudfrontIsDesktopViewer string `json:"cloudfront-is-desktop-viewer"`
			CloudfrontIsMobileViewer  string `json:"cloudfront-is-mobile-viewer"`
			CloudfrontIsSmarttvViewer string `json:"cloudfront-is-smarttv-viewer"`
			CloudfrontViewerCountry   string `json:"cloudfront-viewer-country"`
			CloudfrontIsTabletViewer  string `json:"cloudfront-is-tablet-viewer"`
			ContentLength             string `json:"content-length"`
			ContentType               string `json:"content-type"`
			Host                      string `json:"host"`
			Origin                    string `json:"origin"`
			Referer                   string `json:"Referer"`
			SecFetchDest              string `json:"sec-fetch-dest"`
			SecFetchMode              string `json:"sec-fetch-mode"`
			SecFetchSite              string `json:"sec-fetch-site"`
			UserAgent                 string `json:"user-agent"`
			Via                       string `json:"via"`
			XAmzCfID                  string `json:"x-amz-cf-id"`
			XAmzUserAgent             string `json:"x-amz-user-agent"`
			XAmznTraceID              string `json:"x-amzn-trace-id"`
			XApiKey                   string `json:"x-api-key"`
			XForwardedFor             string `json:"x-forwarded-for"`
			XForwardedPort            string `json:"x-forwarded-port"`
			XForwardedProto           string `json:"x-forwarded-proto"`
			XAmznRequestId            string `json:"x-amzn-requestid"`
		}
		DomainName string `json:"domainName"`
	}
	Source string            `json:"source"`
	Stash  map[string]string `json:"stash"`
}

type InitPaymentResponse struct {
	OrderId string `json:"orderId"`
	URL     string `json:"url"`
}

type Trip struct {
	Id                          string  `json:"id"`
	AvailableRegularTickets     int     `json:"available_regular_tickets"`
	TicketsReservedBySharelead  int     `json:"tickets_reserved"`
	RegularTicketPrice          float64 `json:"regular_ticket_price"`
	ShareleadTotalPayableAmount float64 `json:"sharelead_total_payable_amount"`
	BookingReference            string  `json:"booking_reference"`
}

type VippsConfig struct {
	ClientId            string
	ClientSecret        string
	SubscriptionKey     string
	MSN                 string
	CallbackPrefix      string
	Fallback            string
	SystemName          string
	SystemVersion       string
	SystemPluginName    string
	SystemPluginVersion string
}
type AccessToken struct {
	TokenType    string `json:"token_type"`
	ExpiresIn    string `json:"expires_in"`
	ExtExpiresIn string `json:"ext_expires_in"`
	ExpiresOn    string `json:"expires_on"`
	NotBefore    string `json:"not_before"`
	Resource     string `json:"resource"`
	AccessToken  string `json:"access_token"`
}

type PaymentInitPayload struct {
	MerchantInfo struct {
		AuthToken            string `json:"authToken"`
		CallbackPrefix       string `json:"callbackPrefix"`
		FallBack             string `json:"fallBack"`
		IsApp                bool   `json:"isApp"`
		MerchantSerialNumber string `json:"merchantSerialNumber"`
	} `json:"merchantInfo"`
	CustomerInfo struct {
		MobileNumber string `json:"mobileNumber"`
	} `json:"customerInfo"`
	Transaction struct {
		Amount          float64 `json:"amount"`
		OrderId         string  `json:"orderId"`
		TransactionText string  `json:"transactionText"`
		SkipLandingPage bool    `json:"skipLandingPage"`
	} `json:"transaction"`
}

type TransactionTableModelStatusEnum string

const (
	INITIATE TransactionTableModelStatusEnum = "INITIATE"
	RESERVE  TransactionTableModelStatusEnum = "RESERVE"
	CAPTURE  TransactionTableModelStatusEnum = "CAPTURE"
	FAILED   TransactionTableModelStatusEnum = "FAILED"
	TIMEOUT  TransactionTableModelStatusEnum = "TIMEOUT"
)

type TransactionTableModel struct {
	TripId        string                          `json:"trip_id"`
	TransactionId string                          `json:"transaction_id"`
	Amount        float64                         `json:"amount"`
	Status        TransactionTableModelStatusEnum `json:"status"`         //INITIATE/RESERVE/CAPTURE/FAILED/TIMEOUT
	PaymentWith   string                          `json:"payment_with"`   //VIPPS/STRIPE
	Requester     string                          `json:"requester"`      //cognito id
	CreatedAt     int                             `json:"created_at"`     //timestamp
	ReferenceData []interface{}                   `json:"reference_data"` //Polling data from Vipps
}

type TicketTableModel struct {
	TripId        string  `json:"trip_id" binding:"required"`
	TicketId      string  `json:"ticket_id"` //uuid.hex
	TicketPrice   float64 `json:"ticket_price"`
	Sequence      int     `json:"sequence"`
	Status        string  `json:"status"`        //BLOCKED/AVAIALBLE/CACELLED/PENDING
	BlockTimeout  int     `json:"block_timeout"` //current_time+10mins
	Requester     string  `json:"requester"`     //cognito id
	ContactPerson struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		Email string `json:"email"`
	} `json:"contact_person"` //cognito profile {name, phone}
	CreatedAt   int    `json:"created_at"`   //timestamp
	DownloadURL string `json:"download_url"` //Polling data from Vipps
}

var svc dynamodb.DynamoDB
var appEnv string
var accessTokenResponse AccessToken
var vippsConfig VippsConfig

func main() {
	appEnv = os.Getenv("ENV")
	vippsConfig = VippsConfig{
		ClientId:            os.Getenv("VIPPS_CLIENT_ID"),
		ClientSecret:        os.Getenv("VIPPS_CLIENT_SECRET"),
		SubscriptionKey:     os.Getenv("VIPPS_SUBSCRIPTION_KEY"),
		MSN:                 os.Getenv("VIPPS_MSN"),
		CallbackPrefix:      os.Getenv("VIPPS_CALLBACK_PREFIX"),
		Fallback:            os.Getenv("VIPPS_FALLBACK"),
		SystemName:          "sharebus-sharelead-flow",
		SystemVersion:       "0.1",
		SystemPluginName:    "vipps-sharebus",
		SystemPluginVersion: "0.1",
	}
	accessTokenResponse, _ = getAccessToken() //Access Token

	// Initialize a session that the SDK will use to load
	// credentials from the shared credentials file ~/.aws/credentials
	// and region from the shared configuration file ~/.aws/config
	session := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	// Create DynamoDB client
	svc = *dynamodb.New(session)

	lambda.Start(Handler)
}

func Handler(req json.RawMessage) (InitPaymentResponse, error) {
	var reqData Event
	payloadErr := json.Unmarshal(req, &reqData)
	if payloadErr != nil {
		log.Fatalln(reqData)
		return InitPaymentResponse{}, payloadErr
	}

	trip, tripErr := getTripData(reqData)
	if tripErr != nil {
		return InitPaymentResponse{}, tripErr
	}

	// Ticket reservation
	if trip.TicketsReservedBySharelead > 0 && trip.AvailableRegularTickets >= trip.TicketsReservedBySharelead {
		// deduct reserved number of tickets from available regular ticket count for ticket blocking
		newAvailableRegularTickets := trip.AvailableRegularTickets - trip.TicketsReservedBySharelead
		// after ducduction update the new available regular ticket in trip table
		_, updateTripErr := updateTripData(reqData, newAvailableRegularTickets)
		if updateTripErr != nil {
			return InitPaymentResponse{}, updateTripErr
		}

		generateTickets(&reqData, &trip)
	}

	// Payment
	if trip.ShareleadTotalPayableAmount > 0 {
		//! Sharelead needs to pay
		result, err := initPayment(reqData, trip)
		return result, err
	} else {
		return InitPaymentResponse{}, nil
	}
}

func getTripData(payload Event) (Trip, error) {
	var Error error

	dbResult, err := svc.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(appEnv + "_triptable"),
		Key: map[string]*dynamodb.AttributeValue{
			"id": {
				S: aws.String(payload.Arguments.Input["trip_id"]),
			},
		},
	})
	if err != nil {
		Error = err
		log.Fatalf(Error.Error())
	}

	if dbResult.Item == nil {
		Error = errors.New("Could not find trip with this id: '" + payload.Arguments.Input["trip_id"] + "'")
		log.Fatalf("%s", Error.Error())
	}

	result := Trip{}

	err = dynamodbattribute.UnmarshalMap(dbResult.Item, &result)
	if err != nil {
		Error = err
		log.Fatalf("Trip data response unmarshal err: %s", err)
	}

	return result, Error
}

func updateTripData(reqData Event, newAvailableRegularTickets int) (bool, error) {
	var Error error

	input := &dynamodb.UpdateItemInput{
		TableName: aws.String(appEnv + "_triptable"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":t": {
				N: aws.String(strconv.Itoa(newAvailableRegularTickets)),
			},
		},
		Key: map[string]*dynamodb.AttributeValue{
			"id": {
				S: aws.String(reqData.Arguments.Input["trip_id"]),
			},
		},
		ReturnValues:     aws.String("UPDATED_NEW"),
		UpdateExpression: aws.String("set available_regular_tickets = :t"),
	}

	_, err := svc.UpdateItem(input)
	if err != nil {
		Error = err
		log.Fatalf(Error.Error())
		return false, Error
	}

	return true, Error
}

func getAccessToken() (AccessToken, error) {
	url := "https://apitest.vipps.no/accessToken/get"
	method := "POST"

	payload := strings.NewReader(``)

	client := &http.Client{}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		log.Fatalf("Get access token new request make err: %s", err)
		return AccessToken{}, err
	}

	req.Header.Add("client_id", vippsConfig.ClientId)
	req.Header.Add("client_secret", vippsConfig.ClientSecret)
	req.Header.Add("Ocp-Apim-Subscription-Key", vippsConfig.SubscriptionKey)
	req.Header.Add("Merchant-Serial-Number", vippsConfig.MSN)
	req.Header.Add("Vipps-System-Name", vippsConfig.SystemName)
	req.Header.Add("Vipps-System-Version", vippsConfig.SystemVersion)
	req.Header.Add("Vipps-System-Plugin-Name", vippsConfig.SystemPluginName)
	req.Header.Add("Vipps-System-Plugin-Version", vippsConfig.SystemPluginVersion)

	res, err := client.Do(req)
	if err != nil {
		log.Fatalf("Get access token API call err: %s", err)
		return AccessToken{}, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Fatalf("Get access token req body read err: %s", err)
		return AccessToken{}, err
	}

	var accessTokenResponse AccessToken
	accessTokenResponseErr := json.Unmarshal(body, &accessTokenResponse)
	if accessTokenResponseErr != nil {
		log.Fatalf("Access token unmarshal err: %s", accessTokenResponseErr)
		return AccessToken{}, accessTokenResponseErr
	}

	return accessTokenResponse, nil
}

func initPayment(reqData Event, trip Trip) (InitPaymentResponse, error) {
	url := "https://apitest.vipps.no/ecomm/v2/payments/"
	method := "POST"

	newPayload := PaymentInitPayload{}
	// newPayload.MerchantInfo.AuthToken = "7b0b703f-12b1-4f30-9f60-2973088cb933" // for the callback authrization
	newPayload.MerchantInfo.CallbackPrefix = vippsConfig.CallbackPrefix
	newPayload.MerchantInfo.FallBack = vippsConfig.Fallback
	newPayload.MerchantInfo.IsApp = false
	newPayload.MerchantInfo.MerchantSerialNumber = vippsConfig.MSN
	// newPayload.CustomerInfo.MobileNumber = "98258879" // no need to pass
	newPayload.Transaction.Amount = trip.ShareleadTotalPayableAmount
	newPayload.Transaction.OrderId = fmt.Sprintf("%s%d", trip.BookingReference+"-", time.Now().Unix()) //generated a transaction id with unix timestamp
	newPayload.Transaction.TransactionText = "Transaction initiated through sharebus backend"
	newPayload.Transaction.SkipLandingPage = false

	data, err := json.Marshal(newPayload)
	if err != nil {
		panic(err)
	}

	payload := strings.NewReader(string(data))

	client := &http.Client{}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		log.Fatalf("Init payment new request make err: %s", err)
		return InitPaymentResponse{}, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Ocp-Apim-Subscription-Key", vippsConfig.SubscriptionKey)
	req.Header.Add("Authorization", accessTokenResponse.TokenType+" "+accessTokenResponse.AccessToken)
	req.Header.Add("Merchant-Serial-Number", vippsConfig.MSN)
	req.Header.Add("Vipps-System-Name", vippsConfig.SystemName)
	req.Header.Add("Vipps-System-Version", vippsConfig.SystemVersion)
	req.Header.Add("Vipps-System-Plugin-Name", vippsConfig.SystemPluginName)
	req.Header.Add("Vipps-System-Plugin-Version", vippsConfig.SystemPluginVersion)

	res, err := client.Do(req)
	if err != nil {
		log.Fatalf("Init payment API call err: %s", err)
		return InitPaymentResponse{}, err
	}

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Fatalf("Init payment req body read err: %s", err)
		return InitPaymentResponse{}, err
	}

	var initPaymentResponse InitPaymentResponse
	initPaymentResponseErr := json.Unmarshal(body, &initPaymentResponse)
	if initPaymentResponseErr != nil {
		log.Fatalf("Init payment res unmarshal err: %s", initPaymentResponseErr)
		return InitPaymentResponse{}, initPaymentResponseErr
	}

	// save taransaction info into transaction table
	saveDataToTransactionTable(&reqData, &trip, &initPaymentResponse)

	return initPaymentResponse, nil
}

func saveDataToTransactionTable(reqData *Event, trip *Trip, vippsResponseData *InitPaymentResponse) {
	transaction := TransactionTableModel{
		TripId:        trip.Id,
		TransactionId: vippsResponseData.OrderId,
		Amount:        trip.ShareleadTotalPayableAmount,
		Status:        TransactionTableModelStatusEnum(INITIATE),
		PaymentWith:   "VIPPS",
		Requester:     reqData.Identity.Sub,
		CreatedAt:     int(time.Now().Unix()),
	}

	tableAttribute, err := dynamodbattribute.MarshalMap(transaction)
	if err != nil {
		log.Fatalf("Got error marshalling new transaction item: %s", err)
	}

	input := &dynamodb.PutItemInput{
		TableName: aws.String(appEnv + "_transactions"),
		Item:      tableAttribute,
	}

	_, err = svc.PutItem(input)
	if err != nil {
		log.Fatalf("Got error calling PutItem in transaction table: %s", err)
	}
}

func generateTickets(reqData *Event, trip *Trip) {
	ticket := TicketTableModel{
		TripId:       trip.Id,
		TicketPrice:  trip.RegularTicketPrice,
		Status:       "BLOCKED",
		BlockTimeout: int(time.Now().Unix()) + (10 * 60),
		Requester:    reqData.Identity.Sub,
		CreatedAt:    int(time.Now().Unix()),
		DownloadURL:  "",
	}
	ticket.ContactPerson.Name = reqData.Identity.Claims.Name
	ticket.ContactPerson.Email = reqData.Identity.Claims.Email
	ticket.ContactPerson.Phone = reqData.Identity.Claims.PhoneNumber

	for i := 1; i <= trip.TicketsReservedBySharelead; i++ {
		ticket.TicketId = uuid.New().String()
		ticket.Sequence = i

		av, err := dynamodbattribute.MarshalMap(ticket)
		if err != nil {
			log.Fatalf("Got error marshalling map: %s", err)
		}

		// Create item in table
		input := &dynamodb.PutItemInput{
			Item:      av,
			TableName: aws.String(appEnv + "_tickets"),
		}

		_, err = svc.PutItem(input)
		if err != nil {
			log.Fatalf("Got error calling ticket table PutItem: %s", err)
		}
	}
}

