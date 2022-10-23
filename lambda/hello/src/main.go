package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
)

type Event struct {
	Arguments struct {
		Input map[string]string `json:"input"`
	} `json:"arguments"`
	Identity struct {
		Claims struct {
			Sub             string `json:"sub"`
			CognitoUsername string `json:"cognito:username"`
			PhoneNumber     string `json:"phone_number"`
			Email           string `json:"email"`
			Name            string `json:"name"`
		} `json:"claims"`
		Issuer   string `json:"issuer"`
		Sub      string `json:"sub"`
		Username string `json:"username"`
	} `json:"identity"`
	Request struct {
		Headers struct {
			Authorization string `json:"authorization"`
		} `json:"headers"`
	} `json:"request"`
}

type Response struct {
	VippsRedirectURL string `json:"vipps_redirect_url"`
	TransactionId    string `json:"transaction_id"`
	IsError          bool   `json:"is_error"`
	Message          string `json:"message"`
}

type Trip struct {
	Id                        string   `json:"id"`
	AvailableEarlybirdTickets int      `json:"available_earlybird_tickets"`
	EarlybirdTicketPrice      float64  `json:"earlybird_ticket_price"`
	AvailableRegularTickets   int      `json:"available_regular_tickets"`
	RegularTicketPrice        float64  `json:"regular_ticket_price"`
	TotalCancelledTickets     int      `json:"total_cancelled_tickets"`
	TotalSoldTickets          int      `json:"total_sold_tickets"`
	BookingReference          string   `json:"booking_reference"`
	ParticipantIDs            []string `json:"participant_ids"`
}

type VippsConfig struct {
	ClientId            string
	ClientSecret        string
	SubscriptionKey     string
	MSN                 string
	AccessTokenApiUrl   string
	InitiateApiUrl      string
	Fallback            string
	CallbackPrefix      string
	SystemName          string
	SystemVersion       string
	SystemPluginName    string
	SystemPluginVersion string
}
type VippsAccessToken struct {
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

type InitPaymentResponse struct {
	OrderId string `json:"orderId"`
	URL     string `json:"url"`
}

type TransactionStatusEnum string

const (
	INITIATED        TransactionStatusEnum = "INITIATED"
	PAID             TransactionStatusEnum = "PAID"
	CANCELED         TransactionStatusEnum = "CANCELED"
	FAILED           TransactionStatusEnum = "FAILED"
	REFUND_INITIATED TransactionStatusEnum = "REFUND_INITIATED"
	REFUNDED         TransactionStatusEnum = "REFUNDED"
)

type TransactionTableModel struct {
	Id            string                `json:"id"`
	TripId        string                `json:"trip_id"`
	Amount        float64               `json:"amount"`
	Status        TransactionStatusEnum `json:"status"`       //INITIATED/PAID/CANCELED/FAILED/REFUND_INITIATED/REFUNDED
	PaymentWith   string                `json:"payment_with"` //VIPPS/STRIPE
	Requester     string                `json:"requester"`    //cognito id
	ContactPerson struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		Email string `json:"email"`
	} `json:"contact_person"` //cognito profile {name, phone}
	VippsCallbackAuthToken string        `json:"vipps_callback_auth_token"` // require for authenticate callback
	CreatedAt              int           `json:"created_at"`                //timestamp
	ReferenceData          []interface{} `json:"reference_data"`            //Polling data from Vipps
}

type TicketTypeEnum string

const (
	EARLYBIRD TicketTypeEnum = "EARLYBIRD"
	REGULAR   TicketTypeEnum = "REGULAR"
)

type TicketTableModel struct {
	TripId        string         `json:"trip_id" binding:"required"`
	TicketId      string         `json:"ticket_id"` //uuid.hex
	TicketPrice   float64        `json:"ticket_price"`
	Sequence      int            `json:"sequence"`
	Status        string         `json:"status"`        //BLOCKED/BOOKED/CANCELED
	Type          TicketTypeEnum `json:"type"`          // EARLYBIRD/REGULAR
	BlockTimeout  int            `json:"block_timeout"` //current_time+10mins
	TransactionId string         `json:"transaction_id"`
	Requester     string         `json:"requester"` //cognito id
	ContactPerson struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		Email string `json:"email"`
	} `json:"contact_person"` //cognito profile {name, phone}
	CreatedAt   int    `json:"created_at"`   //timestamp
	DownloadURL string `json:"download_url"` //Polling data from Vipps
}

var dynamoDBClient dynamodb.DynamoDB
var cognitoClient cognitoidentityprovider.CognitoIdentityProvider
var appEnv string
var vippsAccessTokenResponse VippsAccessToken
var vippsConfig VippsConfig

func main() {
	appEnv = os.Getenv("ENV")
	vippsConfig = VippsConfig{
		ClientId:            os.Getenv("VIPPS_CLIENT_ID"),
		ClientSecret:        os.Getenv("VIPPS_CLIENT_SECRET"),
		SubscriptionKey:     os.Getenv("VIPPS_SUBSCRIPTION_KEY"),
		MSN:                 os.Getenv("VIPPS_MSN"),
		AccessTokenApiUrl:   os.Getenv("VIPPS_ACCESS_TOKEN_API"),
		InitiateApiUrl:      os.Getenv("VIPPS_PAYMENT_API"),
		Fallback:            os.Getenv("VIPPS_FALLBACK"),
		CallbackPrefix:      os.Getenv("VIPPS_CALLBACK_PREFIX"),
		SystemName:          "sharebus-joiner",
		SystemVersion:       "2.0",
		SystemPluginName:    "vipps-sharebus",
		SystemPluginVersion: "2.0",
	}
	vippsAccessTokenResponse, _ = getVippsAccessToken()

	// Initialize a session that the SDK will use to load
	// credentials from the shared credentials file ~/.aws/credentials
	// and region from the shared configuration file ~/.aws/config
	session := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	// Create DynamoDB client
	dynamoDBClient = *dynamodb.New(session)

	// Create cognito client
	cognitoClient = *cognitoidentityprovider.New(session)

	lambda.Start(Handler)
}

func Handler(req json.RawMessage) (Response, error) {
	var reqData Event
	payloadErr := json.Unmarshal(req, &reqData)
	if payloadErr != nil {
		fmt.Printf("req data unmarshal err: %+v", reqData)
		return Response{IsError: true, Message: payloadErr.Error()}, nil
	}

	// save input data into variable
	earlybirdTicketsFromReqBody, _ := strconv.Atoi(reqData.Arguments.Input["early_bird_tickets"]) //180
	regularTicketsFromReqBody, _ := strconv.Atoi(reqData.Arguments.Input["regular_tickets"])      //200
	totalPriceFromReqBody, _ := strconv.ParseFloat(reqData.Arguments.Input["total_price"], 64)

	// get requester indentity from cognito and set into reqData
	setRequesterIdentityInRequestDataFromCognito(&reqData)

	// get trip data
	trip, tripErr := getTripData(&reqData)
	if tripErr != nil {
		return Response{IsError: true, Message: tripErr.Error()}, nil
	}

	//* ==> Validate price
	if (trip.EarlybirdTicketPrice*float64(earlybirdTicketsFromReqBody))+(trip.RegularTicketPrice*float64(regularTicketsFromReqBody)) == totalPriceFromReqBody {
		//* price validated <==

		//* ==> Validate ticket availability
		if trip.AvailableRegularTickets < regularTicketsFromReqBody {
			return Response{IsError: true, Message: "There is not a sufficient regular ticket to book."}, nil
		}
		if trip.AvailableEarlybirdTickets < earlybirdTicketsFromReqBody {
			return Response{IsError: true, Message: "There is not a sufficient earlybird ticket to book."}, nil
		}
		//* Ticket availability Validated <==

		//* ==> update ticket counts into trip table with conditional update
		updateTripErr := updateTrip(&reqData, &trip, regularTicketsFromReqBody, earlybirdTicketsFromReqBody)
		if updateTripErr != nil {
			return Response{IsError: true, Message: updateTripErr.Error()}, nil
		}
		//* trip table updated <==

		//* ==> initiate payment
		initPaymentResult, initPaymentErr := initPaymentAndSaveIntoTransactionTable(&reqData, &trip, totalPriceFromReqBody)
		if initPaymentErr != nil {
			return Response{IsError: true, Message: initPaymentErr.Error()}, nil
		}
		//* payment initiated <==

		//* ==> Save participant id into trip table if new participant
		handleParticipantsErr := handleParticipants(&reqData, &trip)
		if handleParticipantsErr != nil {
			return Response{IsError: true, Message: handleParticipantsErr.Error()}, nil
		}
		//* participant id Handled <==

		//* ==> Generate tickets as blocked and insert into ticket table
		generateTicketsErr := generateTicketsAndSaveIntoTicketTable(&reqData, &trip, initPaymentResult.OrderId, regularTicketsFromReqBody, earlybirdTicketsFromReqBody)
		if generateTicketsErr != nil {
			return Response{IsError: true, Message: generateTicketsErr.Error()}, nil
		}
		//* ==> Tickets Generation completed

		return Response{VippsRedirectURL: initPaymentResult.URL, TransactionId: initPaymentResult.OrderId, IsError: false, Message: "Tickets successfully booked."}, nil
	} else {
		return Response{IsError: true, Message: "The price is mismatched, please provide a valid price."}, nil
	}
}

func getTripData(reqData *Event) (Trip, error) {
	var Error error

	dbResult, err := dynamoDBClient.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(appEnv + "_trips"),
		Key: map[string]*dynamodb.AttributeValue{
			"id": {
				S: aws.String(reqData.Arguments.Input["trip_id"]),
			},
		},
	})
	if err != nil {
		Error = err
		fmt.Printf("trip db get query err: %s", Error.Error())
	}

	if dbResult.Item == nil {
		Error = errors.New("Could not find trip with this id: '" + reqData.Arguments.Input["trip_id"] + "'")
		fmt.Printf("%s", Error.Error())
	}

	result := Trip{}

	err = dynamodbattribute.UnmarshalMap(dbResult.Item, &result)
	if err != nil {
		Error = err
		fmt.Printf("Trip data response unmarshal err: %s", err)
	}

	return result, Error
}

func updateTrip(reqData *Event, trip *Trip, regularTicketsFromReqBody int, earlybirdTicketsFromReqBody int) error {
	expressionAttributeValues := map[string]*dynamodb.AttributeValue{
		":soldrt": {
			N: aws.String(strconv.Itoa(-regularTicketsFromReqBody)),
		},
		":soldebt": {
			N: aws.String(strconv.Itoa(-earlybirdTicketsFromReqBody)),
		},
		":exart": {
			N: aws.String(strconv.Itoa(trip.AvailableRegularTickets)),
		},
		":exebt": {
			N: aws.String(strconv.Itoa(trip.AvailableEarlybirdTickets)),
		},
	}
	expressionAttributeNames := map[string]*string{
		"#ART":  aws.String("available_regular_tickets"),
		"#AEBT": aws.String("available_earlybird_tickets"),
	}
	updateExpression := aws.String("ADD #ART :soldrt, #AEBT :soldebt")
	conditionExpression := aws.String("#ART = :exart AND #AEBT = :exebt")

	input := &dynamodb.UpdateItemInput{
		TableName: aws.String(appEnv + "_trips"),
		Key: map[string]*dynamodb.AttributeValue{
			"id": {
				S: aws.String(reqData.Arguments.Input["trip_id"]),
			},
		},
		ExpressionAttributeValues: expressionAttributeValues,
		ExpressionAttributeNames:  expressionAttributeNames,
		UpdateExpression:          updateExpression,
		ConditionExpression:       conditionExpression,
		ReturnValues:              aws.String("UPDATED_NEW"),
	}

	_, err := dynamoDBClient.UpdateItem(input)
	if err != nil {
		fmt.Printf("trip db update query err: %s", err.Error())
		return err
	}

	return nil
}

func getVippsAccessToken() (VippsAccessToken, error) {
	url := vippsConfig.AccessTokenApiUrl
	method := "POST"

	payload := strings.NewReader(``)

	client := &http.Client{}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		fmt.Printf("Get access token new request make err: %s", err)
		return VippsAccessToken{}, err
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
		fmt.Printf("Get access token API call err: %s", err)
		return VippsAccessToken{}, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Printf("Get access token req body read err: %s", err)
		return VippsAccessToken{}, err
	}

	var vippsAccessTokenResponse VippsAccessToken
	vippsAccessTokenResponseErr := json.Unmarshal(body, &vippsAccessTokenResponse)
	if vippsAccessTokenResponseErr != nil {
		fmt.Printf("Access token unmarshal err: %s", vippsAccessTokenResponseErr)
		return VippsAccessToken{}, vippsAccessTokenResponseErr
	}

	return vippsAccessTokenResponse, nil
}

func initPaymentAndSaveIntoTransactionTable(reqData *Event, trip *Trip, payableAmount float64) (InitPaymentResponse, error) {
	url := vippsConfig.InitiateApiUrl
	method := "POST"

	transactionId := fmt.Sprintf("%s%d", trip.BookingReference+"-"+GenerateRandomString(4)+"-", time.Now().Unix()) //generated a transaction id with unix timestamp

	paymentInitPayload := PaymentInitPayload{}
	paymentInitPayload.MerchantInfo.AuthToken = uuid.New().String() // for the callback authrization
	paymentInitPayload.MerchantInfo.CallbackPrefix = vippsConfig.CallbackPrefix
	paymentInitPayload.MerchantInfo.FallBack = vippsConfig.Fallback + "?tripId=" + trip.Id + "&transactionId=" + transactionId
	paymentInitPayload.MerchantInfo.IsApp = false
	paymentInitPayload.MerchantInfo.MerchantSerialNumber = vippsConfig.MSN
	paymentInitPayload.CustomerInfo.MobileNumber = reqData.Identity.Claims.PhoneNumber
	paymentInitPayload.Transaction.Amount = payableAmount * 100 //Convert krona to öre
	paymentInitPayload.Transaction.OrderId = transactionId
	paymentInitPayload.Transaction.TransactionText = "Transaction initiated through sharebus backend"
	paymentInitPayload.Transaction.SkipLandingPage = false

	data, err := json.Marshal(paymentInitPayload)
	if err != nil {
		fmt.Printf("Init payment body marshall err: %s", err)
		return InitPaymentResponse{}, err
	}

	payload := strings.NewReader(string(data))

	client := &http.Client{}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		fmt.Printf("Init payment new request make err: %s", err)
		return InitPaymentResponse{}, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Ocp-Apim-Subscription-Key", vippsConfig.SubscriptionKey)
	req.Header.Add("Authorization", vippsAccessTokenResponse.TokenType+" "+vippsAccessTokenResponse.AccessToken)
	req.Header.Add("Merchant-Serial-Number", vippsConfig.MSN)
	req.Header.Add("Vipps-System-Name", vippsConfig.SystemName)
	req.Header.Add("Vipps-System-Version", vippsConfig.SystemVersion)
	req.Header.Add("Vipps-System-Plugin-Name", vippsConfig.SystemPluginName)
	req.Header.Add("Vipps-System-Plugin-Version", vippsConfig.SystemPluginVersion)

	res, err := client.Do(req)
	if err != nil {
		fmt.Printf("Init payment API call err: %s", err)
		return InitPaymentResponse{}, err
	}

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Printf("Init payment req body read err: %s", err)
		return InitPaymentResponse{}, err
	}

	var initPaymentResponse InitPaymentResponse
	initPaymentResponseErr := json.Unmarshal(body, &initPaymentResponse)
	if initPaymentResponseErr != nil {
		fmt.Printf("Init payment res unmarshal err: %s", initPaymentResponseErr)
		return InitPaymentResponse{}, initPaymentResponseErr
	}

	// save taransaction info into transaction table
	saveDataToTransactionTableErr := saveDataToTransactionTable(reqData, trip, &paymentInitPayload)
	if saveDataToTransactionTableErr != nil {
		return InitPaymentResponse{}, saveDataToTransactionTableErr
	}
	return initPaymentResponse, nil
}

func GenerateRandomString(length int) string {
	var table = [...]byte{'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O', 'P',
		'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j',
		'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
		'1', '2', '3', '4', '5', '6', '7', '8', '9', '0'}
	b := make([]byte, length)
	n, err := io.ReadAtLeast(rand.Reader, b, length)
	if n != length {
		fmt.Println("Random string generation err:", err)
	}
	for i := 0; i < len(b); i++ {
		b[i] = table[int(b[i])%len(table)]
	}
	return string(b)
}

func saveDataToTransactionTable(reqData *Event, trip *Trip, paymentInitPayload *PaymentInitPayload) error {
	transaction := TransactionTableModel{
		Id:                     paymentInitPayload.Transaction.OrderId,
		TripId:                 trip.Id,
		Amount:                 paymentInitPayload.Transaction.Amount,
		Status:                 TransactionStatusEnum(INITIATED),
		PaymentWith:            "VIPPS",
		VippsCallbackAuthToken: paymentInitPayload.MerchantInfo.AuthToken,
		Requester:              reqData.Identity.Sub,
		CreatedAt:              int(time.Now().Unix()),
	}
	transaction.ContactPerson.Name = reqData.Identity.Claims.Name
	transaction.ContactPerson.Email = reqData.Identity.Claims.Email
	transaction.ContactPerson.Phone = reqData.Identity.Claims.PhoneNumber

	tableAttribute, err := dynamodbattribute.MarshalMap(transaction)
	if err != nil {
		fmt.Printf("Got error marshalling new transaction item: %s", err)
		return err
	}

	input := &dynamodb.PutItemInput{
		TableName: aws.String(appEnv + "_transactions"),
		Item:      tableAttribute,
	}

	_, err = dynamoDBClient.PutItem(input)
	if err != nil {
		fmt.Printf("Got error calling PutItem in transaction table: %s", err)
		return err
	}
	return nil
}

func handleParticipants(reqData *Event, trip *Trip) error {
	isThisParticipantContains := Contains(trip.ParticipantIDs, reqData.Identity.Sub)

	if !isThisParticipantContains {
		av := &dynamodb.AttributeValue{
			S: aws.String(reqData.Identity.Sub),
		}
		var qids []*dynamodb.AttributeValue
		qids = append(qids, av)

		input := &dynamodb.UpdateItemInput{
			Key: map[string]*dynamodb.AttributeValue{
				"id": {
					S: aws.String(reqData.Arguments.Input["trip_id"]),
				},
			},
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":qid": {
					L: qids,
				},
				":empty_list": {
					L: []*dynamodb.AttributeValue{},
				},
			},
			ReturnValues:     aws.String("ALL_NEW"),
			UpdateExpression: aws.String(`SET participant_ids = list_append(if_not_exists(participant_ids, :empty_list), :qid)`),
			TableName:        aws.String(appEnv + "_trips"),
		}

		_, err := dynamoDBClient.UpdateItem(input)
		if err != nil {
			fmt.Printf("trip db update query err: %s", err.Error())
			return err
		}
	}
	return nil
}

// Contains tells whether a contains x.
func Contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

func generateTicketsAndSaveIntoTicketTable(reqData *Event, trip *Trip, transactionId string, totalRegularTickets int, totalEarlybirdTickets int) error {
	ticketBlockTimeout, _ := strconv.Atoi(os.Getenv("TICKET_BLOCK_TIMEOUT_IN_MINUTES"))
	ticket := TicketTableModel{
		TripId:        trip.Id,
		Status:        "BLOCKED",
		TransactionId: transactionId,
		BlockTimeout:  int(time.Now().Unix()) + (ticketBlockTimeout * 60),
		Requester:     reqData.Identity.Sub,
		CreatedAt:     int(time.Now().Unix()),
		DownloadURL:   "",
	}
	ticket.ContactPerson.Name = reqData.Identity.Claims.Name
	ticket.ContactPerson.Email = reqData.Identity.Claims.Email
	ticket.ContactPerson.Phone = reqData.Identity.Claims.PhoneNumber

	// Insert Reagular tickets into DB
	for i := 1; i <= totalRegularTickets; i++ {
		ticket.TicketId = uuid.New().String()
		ticket.TicketPrice = trip.RegularTicketPrice
		ticket.Type = TicketTypeEnum(REGULAR)
		ticket.Sequence = i

		av, err := dynamodbattribute.MarshalMap(ticket)
		if err != nil {
			fmt.Printf("Got error marshalling map: %s", err.Error())
			return err
		}

		// Create item in table
		input := &dynamodb.PutItemInput{
			Item:      av,
			TableName: aws.String(appEnv + "_tickets"),
		}

		_, err = dynamoDBClient.PutItem(input)
		if err != nil {
			fmt.Printf("Got error calling ticket table PutItem: %s", err.Error())
			return err
		}
	}

	// Insert Earlybird tickets into DB
	for i := 1; i <= totalEarlybirdTickets; i++ {
		ticket.TicketId = uuid.New().String()
		ticket.TicketPrice = trip.EarlybirdTicketPrice
		ticket.Type = TicketTypeEnum(EARLYBIRD)
		ticket.Sequence = i

		av, err := dynamodbattribute.MarshalMap(ticket)
		if err != nil {
			fmt.Printf("Got error marshalling map: %s", err.Error())
			return err
		}

		// Create item in table
		input := &dynamodb.PutItemInput{
			Item:      av,
			TableName: aws.String(appEnv + "_tickets"),
		}

		_, err = dynamoDBClient.PutItem(input)
		if err != nil {
			fmt.Printf("Got error calling ticket table PutItem: %s", err.Error())
			return err
		}
	}
	return nil
}

func setRequesterIdentityInRequestDataFromCognito(reqData *Event) {
	// Collect userpoolId from request data
	splitedIssuer := strings.Split(reqData.Identity.Issuer, "/")
	var userPoolID *string = &splitedIssuer[len(splitedIssuer)-1]

	// Get user info from cognito
	results, err := cognitoClient.AdminGetUser(&cognitoidentityprovider.AdminGetUserInput{
		UserPoolId: userPoolID,
		Username:   &reqData.Identity.Username,
	})
	if err != nil {
		fmt.Printf("Error getting user from cognito: %+v", err)
	}

	// Assign the user info into request identity
	for _, a := range results.UserAttributes {
		if *a.Name == "name" {
			// ticket.ContactPerson.Name = *a.Value
			reqData.Identity.Claims.Name = *a.Value
		} else if *a.Name == "email" {
			reqData.Identity.Claims.Email = *a.Value
		} else if *a.Name == "phone_number" {
			reqData.Identity.Claims.PhoneNumber = *a.Value
		}
	}
}
