// SPDX-FileCopyrightText: 2021 Open Networking Foundation <info@opennetworking.org>
// Copyright 2019 free5GC.org
//
// SPDX-License-Identifier: Apache-2.0
package producer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	protos "github.com/5GC-DEV/config5g-cdac/proto/sdcoreConfig"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/mitchellh/mapstructure"
	"github.com/omec-project/openapi/models"
	udr_context "github.com/omec-project/udr/context"
	"github.com/omec-project/udr/logger"
	stats "github.com/omec-project/udr/metrics"
	"github.com/omec-project/udr/util"
	"github.com/omec-project/util/httpwrapper"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	APPDATA_INFLUDATA_DB_COLLECTION_NAME       = "applicationData.influenceData"
	APPDATA_INFLUDATA_SUBSC_DB_COLLECTION_NAME = "applicationData.influenceData.subsToNotify"
	APPDATA_PFD_DB_COLLECTION_NAME             = "applicationData.pfds"
	POLICYDATA_BDTDATA                         = "policyData.bdtData"
	POLICYDATA_UES_OPSPECDATA                  = "policyData.ues.operatorSpecificData"
	POLICYDATA_UES_SMDATA_USAGEMONDATA         = "policyData.ues.smData.usageMonData"
	POLICYDATA_UES_UEPOLICYSET                 = "policyData.ues.uePolicySet"
	SUBSCDATA_CTXDATA_AMF_3GPPACCESS           = "subscriptionData.contextData.amf3gppAccess"
	SUBSCDATA_CTXDATA_AMF_NON3GPPACCESS        = "subscriptionData.contextData.amfNon3gppAccess"
	SUBSCDATA_CTXDATA_SMF_REGISTRATION         = "subscriptionData.contextData.smfRegistrations"
	SUBSCDATA_CTXDATA_SMSF_3GPPACCESS          = "subscriptionData.contextData.smsf3gppAccess"
	SUBSCDATA_CTXDATA_SMSF_NON3GPPACCESS       = "subscriptionData.contextData.smsfNon3gppAccess"
)

var CurrentResourceUri string

func getDataFromDB(collName string, filter bson.M) (map[string]interface{}, *models.ProblemDetails) {
	data, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}
	if data == nil {
		return nil, util.ProblemDetailsNotFound("DATA_NOT_FOUND")
	}

	// Delete "_id" entry which is auto-inserted by MongoDB
	delete(data, "_id")
	return data, nil
}

func deleteDataFromDB(collName string, filter bson.M) error {
	errDelOne := CommonDBClient.RestfulAPIDeleteOne(collName, filter)
	if errDelOne != nil {
		logger.DataRepoLog.Warnln(errDelOne)
	}
	return errDelOne
}

func HandleCreateAccessAndMobilityData(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

// seems something which we should move to mongolib
func toBsonM(data interface{}) (ret bson.M) {
	tmp, err := json.Marshal(data)
	if err != nil {
		logger.CfgLog.Infoln("marshal fail", err)
	}
	err = json.Unmarshal(tmp, &ret)
	if err != nil {
		logger.CfgLog.Infoln("unmarshal fail", err)
	}
	return
}

// AddEntrySmPolicyTable ... write table entries into policyData.ues.smData
func AddEntrySmPolicyTable(imsi string, dnn string, snssai *protos.NSSAI) error {
	logger.CfgLog.Infoln("AddEntrySmPolicyTable")
	collName := "policyData.ues.smData"
	var addUeId bool
	logger.CfgLog.Infoln("collname, imsi, dnn, sst, sd:", collName, imsi, dnn, snssai.Sst, snssai.Sd)
	ueID := "imsi-" + imsi

	sval, err := strconv.ParseUint(snssai.Sst, 10, 32)
	if err != nil {
		logger.CfgLog.Errorln("parse fail for sst", err)
		return err
	}
	filter := bson.M{"ueId": ueID}
	modelNssai := models.Snssai{
		Sd:  snssai.Sd,
		Sst: int32(sval),
	}
	smPolicyData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}
	var smPolicyDataWrite models.SmPolicyData
	if smPolicyData != nil {
		err := json.Unmarshal(util.MapToByte(smPolicyData), &smPolicyDataWrite)
		if err != nil {
			logger.DataRepoLog.Warnln(err)
			return err
		}
	} else {
		smPolicyDataWrite.SmPolicySnssaiData = make(map[string]models.SmPolicySnssaiData)
		addUeId = true
	}

	smPolicySnssaiData := models.SmPolicySnssaiData{
		Snssai: &modelNssai,
		SmPolicyDnnData: map[string]models.SmPolicyDnnData{
			dnn: {
				Dnn: dnn,
			},
		},
	}

	// Check if the DNN already exists for this SNSSAI and IMSI
	hexSnssai := util.SnssaiModelsToHex(modelNssai)
	if existingData, exists := smPolicyDataWrite.SmPolicySnssaiData[hexSnssai]; exists {
		// DNN already exists, append to the existing DNN data
		existingData.SmPolicyDnnData[dnn] = models.SmPolicyDnnData{
			Dnn: dnn,
		}
		smPolicyDataWrite.SmPolicySnssaiData[hexSnssai] = existingData
	} else {
		// New DNN for this SNSSAI, so add the new entry
		smPolicyDataWrite.SmPolicySnssaiData[hexSnssai] = smPolicySnssaiData
	}

	// Convert the updated data to BSON format for database insertion
	smPolicyDataBsonM := toBsonM(smPolicyDataWrite)
	if addUeId {
		smPolicyDataBsonM["ueId"] = ueID
	}
	logger.CfgLog.Infof("Data to be sent to database - smPolicyData: %+v", smPolicyDataBsonM)

	// Post the data to the database
	_, errPost := CommonDBClient.RestfulAPIPost(collName, filter, smPolicyDataBsonM)
	if errPost != nil {
		logger.DataRepoLog.Warnln(errPost)
	}
	return nil
}

func HandleDeleteAccessAndMobilityData(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandleQueryAccessAndMobilityData(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandleQueryAmData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryAmData")

	collName := "subscriptionData.provisionedData.amData"
	ueId := request.Params["ueId"]
	servingPlmnId := request.Params["servingPlmnId"]
	response, problemDetails := QueryAmDataProcedure(collName, ueId, servingPlmnId)

	if problemDetails == nil {
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func QueryAmDataProcedure(collName string, ueId string, servingPlmnId string) (*map[string]interface{},
	*models.ProblemDetails,
) {
	filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
	accessAndMobilitySubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}
	if accessAndMobilitySubscriptionData != nil {
		return &accessAndMobilitySubscriptionData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleAmfContext3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle AmfContext3gpp")
	collName := SUBSCDATA_CTXDATA_AMF_3GPPACCESS
	patchItem := request.Body.([]models.PatchItem)
	ueId := request.Params["ueId"]

	problemDetails := AmfContext3gppProcedure(collName, ueId, patchItem)
	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "amf-3gpp-access", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "amf-3gpp-access", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func AmfContext3gppProcedure(collName string, ueId string, patchItem []models.PatchItem) *models.ProblemDetails {
	filter := bson.M{"ueId": ueId}
	origValue, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	patchJSON, err := json.Marshal(patchItem)
	if err != nil {
		logger.DataRepoLog.Errorln(err)
	}
	failure := CommonDBClient.RestfulAPIJSONPatch(collName, filter, patchJSON)

	if failure == nil {
		newValue, errGetOneNew := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOneNew != nil {
			logger.DataRepoLog.Warnln(errGetOneNew)
		}
		PreHandleOnDataChangeNotify(ueId, CurrentResourceUri, patchItem, origValue, newValue)
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandleCreateAmfContext3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateAmfContext3gpp")

	Amf3GppAccessRegistration := request.Body.(models.Amf3GppAccessRegistration)
	ueId := request.Params["ueId"]
	collName := SUBSCDATA_CTXDATA_AMF_3GPPACCESS

	err := CreateAmfContext3gppProcedure(collName, ueId, Amf3GppAccessRegistration)
	if err == nil {
		stats.IncrementUdrSubscriptionDataStats("create", "amf-3gpp-access", "SUCCESS")
	} else {
		stats.IncrementUdrSubscriptionDataStats("create", "amf-3gpp-access", "FAILURE")
	}

	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func CreateAmfContext3gppProcedure(collName string, ueId string,
	Amf3GppAccessRegistration models.Amf3GppAccessRegistration,
) error {
	filter := bson.M{"ueId": ueId}
	putData := util.ToBsonM(Amf3GppAccessRegistration)
	putData["ueId"] = ueId

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	return errPutOne
}

func HandleQueryAmfContext3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryAmfContext3gpp")

	ueId := request.Params["ueId"]
	collName := SUBSCDATA_CTXDATA_AMF_3GPPACCESS

	response, problemDetails := QueryAmfContext3gppProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "amf-3gpp-access", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "amf-3gpp-access", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryAmfContext3gppProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}
	amf3GppAccessRegistration, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if amf3GppAccessRegistration != nil {
		return &amf3GppAccessRegistration, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleAmfContextNon3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle AmfContextNon3gpp")

	ueId := request.Params["ueId"]
	collName := SUBSCDATA_CTXDATA_AMF_NON3GPPACCESS
	patchItem := request.Body.([]models.PatchItem)
	filter := bson.M{"ueId": ueId}

	problemDetails := AmfContextNon3gppProcedure(ueId, collName, patchItem, filter)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "amf-non-3gpp-access", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "amf-non-3gpp-access", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func AmfContextNon3gppProcedure(ueId string, collName string, patchItem []models.PatchItem,
	filter bson.M,
) *models.ProblemDetails {
	origValue, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	patchJSON, err := json.Marshal(patchItem)
	if err != nil {
		logger.DataRepoLog.Error(err)
	}
	failure := CommonDBClient.RestfulAPIJSONPatch(collName, filter, patchJSON)
	if failure == nil {
		newValue, errGetOneNew := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOneNew != nil {
			logger.DataRepoLog.Warnln(errGetOneNew)
		}
		PreHandleOnDataChangeNotify(ueId, CurrentResourceUri, patchItem, origValue, newValue)
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandleCreateAmfContextNon3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateAmfContextNon3gpp")

	AmfNon3GppAccessRegistration := request.Body.(models.AmfNon3GppAccessRegistration)
	collName := SUBSCDATA_CTXDATA_AMF_NON3GPPACCESS
	ueId := request.Params["ueId"]

	err := CreateAmfContextNon3gppProcedure(AmfNon3GppAccessRegistration, collName, ueId)
	if err == nil {
		stats.IncrementUdrSubscriptionDataStats("create", "amf-non-3gpp-access", "SUCCESS")
	} else {
		stats.IncrementUdrSubscriptionDataStats("create", "amf-non-3gpp-access", "FAILURE")
	}

	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func CreateAmfContextNon3gppProcedure(AmfNon3GppAccessRegistration models.AmfNon3GppAccessRegistration,
	collName string, ueId string,
) error {
	putData := util.ToBsonM(AmfNon3GppAccessRegistration)
	putData["ueId"] = ueId
	filter := bson.M{"ueId": ueId}

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	return errPutOne
}

func HandleQueryAmfContextNon3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryAmfContextNon3gpp")

	collName := SUBSCDATA_CTXDATA_AMF_NON3GPPACCESS
	ueId := request.Params["ueId"]

	response, problemDetails := QueryAmfContextNon3gppProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "amf-non-3gpp-access", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "amf-non-3gpp-access", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "amf-non-3gpp-access", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryAmfContextNon3gppProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}
	response, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if response != nil {
		return &response, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleModifyAuthentication(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle ModifyAuthentication")

	collName := "subscriptionData.authenticationData.authenticationSubscription"
	ueId := request.Params["ueId"]
	patchItem := request.Body.([]models.PatchItem)

	problemDetails := ModifyAuthenticationProcedure(collName, ueId, patchItem)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "authentication-subscription", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "authentication-subscription", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func ModifyAuthenticationProcedure(collName string, ueId string, patchItem []models.PatchItem) *models.ProblemDetails {
	filter := bson.M{"ueId": ueId}
	origValue, errGetOne := AuthDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	patchJSON, err := json.Marshal(patchItem)
	if err != nil {
		logger.DataRepoLog.Error(err)
	}
	failure := AuthDBClient.RestfulAPIJSONPatch(collName, filter, patchJSON)

	if failure == nil {
		newValue, errGetOneNew := AuthDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOneNew != nil {
			logger.DataRepoLog.Warnln(errGetOneNew)
		}
		PreHandleOnDataChangeNotify(ueId, CurrentResourceUri, patchItem, origValue, newValue)
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandleQueryAuthSubsData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryAuthSubsData")

	collName := "subscriptionData.authenticationData.authenticationSubscription"
	ueId := request.Params["ueId"]

	response, problemDetails := QueryAuthSubsDataProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "authentication-subscription", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "authentication-subscription", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("update", "authentication-subscription", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryAuthSubsDataProcedure(collName string, ueId string) (map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	authenticationSubscription, errGetOne := AuthDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if authenticationSubscription != nil {
		return authenticationSubscription, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleCreateAuthenticationSoR(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateAuthenticationSoR")
	putData := util.ToBsonM(request.Body)
	ueId := request.Params["ueId"]
	collName := "subscriptionData.ueUpdateConfirmationData.sorData"

	err := CreateAuthenticationSoRProcedure(collName, ueId, putData)
	if err == nil {
		stats.IncrementUdrSubscriptionDataStats("create", "sor-data", "SUCCESS")
	} else {
		stats.IncrementUdrSubscriptionDataStats("create", "sor-data", "FAILURE")
	}

	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func CreateAuthenticationSoRProcedure(collName string, ueId string, putData bson.M) error {
	filter := bson.M{"ueId": ueId}
	putData["ueId"] = ueId

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	return errPutOne
}

func HandleQueryAuthSoR(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryAuthSoR")

	ueId := request.Params["ueId"]
	collName := "subscriptionData.ueUpdateConfirmationData.sorData"

	response, problemDetails := QueryAuthSoRProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sor-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sor-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "sor-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryAuthSoRProcedure(collName string, ueId string) (map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	sorData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if sorData != nil {
		return sorData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleCreateAuthenticationStatus(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateAuthenticationStatus")

	putData := util.ToBsonM(request.Body)
	ueId := request.Params["ueId"]
	collName := "subscriptionData.authenticationData.authenticationStatus"

	err := CreateAuthenticationStatusProcedure(collName, ueId, putData)
	if err == nil {
		stats.IncrementUdrSubscriptionDataStats("create", "authentication-status", "SUCCESS")
	} else {
		stats.IncrementUdrSubscriptionDataStats("create", "authentication-status", "FAILURE")
	}

	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func CreateAuthenticationStatusProcedure(collName string, ueId string, putData bson.M) error {
	filter := bson.M{"ueId": ueId}
	putData["ueId"] = ueId

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	return errPutOne
}

func HandleQueryAuthenticationStatus(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryAuthenticationStatus")

	ueId := request.Params["ueId"]
	collName := "subscriptionData.authenticationData.authenticationStatus"

	response, problemDetails := QueryAuthenticationStatusProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "authentication-status", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "authentication-status", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "authentication-status", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryAuthenticationStatusProcedure(collName string, ueId string) (*map[string]interface{},
	*models.ProblemDetails,
) {
	filter := bson.M{"ueId": ueId}

	authEvent, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if authEvent != nil {
		return &authEvent, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleApplicationDataInfluenceDataGet(queryParams map[string][]string) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle ApplicationDataInfluenceDataGet: queryParams=%#v", queryParams)

	influIDs := queryParams["influence-Ids"]
	dnns := queryParams["dnns"]
	snssais := queryParams["snssais"]
	intGroupIDs := queryParams["internal-Group-Ids"]
	supis := queryParams["supis"]
	if len(influIDs) == 0 && len(dnns) == 0 && len(snssais) == 0 && len(intGroupIDs) == 0 && len(supis) == 0 {
		pd := util.ProblemDetailsMalformedReqSyntax("No query parameters")
		stats.IncrementUdrApplicationDataStats("get", "influence-data", "FAILURE")
		return httpwrapper.NewResponse(int(pd.Status), nil, pd)
	}

	response := getApplicationDataInfluenceDatafromDB(influIDs, dnns, snssais, intGroupIDs, supis)
	stats.IncrementUdrApplicationDataStats("get", "influence-data", "SUCCESS")

	return httpwrapper.NewResponse(http.StatusOK, nil, response)
}

func getApplicationDataInfluenceDatafromDB(influIDs, dnns, snssais,
	intGroupIDs, supis []string,
) []map[string]interface{} {
	filter := bson.M{}
	allInfluDatas, errGetMany := CommonDBClient.RestfulAPIGetMany(APPDATA_INFLUDATA_DB_COLLECTION_NAME, filter)
	if errGetMany != nil {
		logger.DataRepoLog.Warnln(errGetMany)
	}
	var matchedInfluDatas []map[string]interface{}
	matchedInfluDatas = filterDataByString("influenceId", influIDs, allInfluDatas)
	matchedInfluDatas = filterDataByString("dnn", dnns, matchedInfluDatas)
	matchedInfluDatas = filterDataByString("interGroupId", intGroupIDs, matchedInfluDatas)
	matchedInfluDatas = filterDataByString("supi", supis, matchedInfluDatas)
	matchedInfluDatas = filterDataBySnssai(snssais, matchedInfluDatas)
	for i := 0; i < len(matchedInfluDatas); i++ {
		// Delete "_id" entry which is auto-inserted by MongoDB
		delete(matchedInfluDatas[i], "_id")
		// Delete "influenceId" entry which is added by us
		delete(matchedInfluDatas[i], "influenceId")
	}
	return matchedInfluDatas
}

func filterDataByString(filterName string, filterValues []string,
	datas []map[string]interface{},
) []map[string]interface{} {
	if len(filterValues) == 0 {
		return datas
	}
	var matchedDatas []map[string]interface{}
	for _, data := range datas {
		for _, v := range filterValues {
			if data[filterName].(string) == v {
				matchedDatas = append(matchedDatas, data)
				break
			}
		}
	}
	return matchedDatas
}

func filterDataBySnssai(snssaiValues []string,
	datas []map[string]interface{},
) []map[string]interface{} {
	if len(snssaiValues) == 0 {
		return datas
	}
	var matchedDatas []map[string]interface{}
	for _, data := range datas {
		var dataSnssai models.Snssai
		if err := json.Unmarshal(
			util.MapToByte(data["snssai"].(map[string]interface{})), &dataSnssai); err != nil {
			logger.DataRepoLog.Warnln(err)
			break
		}
		logger.DataRepoLog.Debugf("dataSnssai=%#v", dataSnssai)
		for _, v := range snssaiValues {
			var filterSnssai models.Snssai
			if err := json.Unmarshal([]byte(v), &filterSnssai); err != nil {
				logger.DataRepoLog.Warnln(err)
				break
			}
			logger.DataRepoLog.Debugf("filterSnssai=%#v", filterSnssai)
			if dataSnssai.Sd == filterSnssai.Sd && dataSnssai.Sst == filterSnssai.Sst {
				matchedDatas = append(matchedDatas, data)
				break
			}
		}
	}
	return matchedDatas
}

func HandleApplicationDataInfluenceDataInfluenceIdDelete(influID string) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataInfluenceDataInfluenceIdDelete: influID=%q", influID)

	deleteApplicationDataIndividualInfluenceDataFromDB(influID)

	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func deleteApplicationDataIndividualInfluenceDataFromDB(influID string) {
	filter := bson.M{"influenceId": influID}
	err := deleteDataFromDB(APPDATA_INFLUDATA_DB_COLLECTION_NAME, filter)
	if err == nil {
		stats.IncrementUdrApplicationDataStats("delete", "influence-data", "SUCCESS")
	} else {
		stats.IncrementUdrApplicationDataStats("delete", "influence-data", "FAILURE")
	}
}

func HandleApplicationDataInfluenceDataInfluenceIdPatch(influID string,
	trInfluDataPatch *models.TrafficInfluDataPatch,
) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataInfluenceDataInfluenceIdPatch: influID=%q", influID)

	response, status := patchApplicationDataIndividualInfluenceDataToDB(influID, trInfluDataPatch)
	stats.IncrementUdrApplicationDataStats("update", "influence-data", "SUCCESS")

	return httpwrapper.NewResponse(status, nil, response)
}

func patchApplicationDataIndividualInfluenceDataToDB(influID string,
	trInfluDataPatch *models.TrafficInfluDataPatch,
) (bson.M, int) {
	filter := bson.M{"influenceId": influID}

	oldData, errGetOne := CommonDBClient.RestfulAPIGetOne(APPDATA_INFLUDATA_DB_COLLECTION_NAME, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}
	if oldData == nil {
		return nil, http.StatusNotFound
	}

	trInfluData := models.TrafficInfluData{
		UpPathChgNotifCorreId: trInfluDataPatch.UpPathChgNotifCorreId,
		AppReloInd:            trInfluDataPatch.AppReloInd,
		AfAppId:               oldData["afAppId"].(string),
		Dnn:                   trInfluDataPatch.Dnn,
		EthTrafficFilters:     trInfluDataPatch.EthTrafficFilters,
		Snssai:                trInfluDataPatch.Snssai,
		InterGroupId:          trInfluDataPatch.InternalGroupId,
		Supi:                  trInfluDataPatch.Supi,
		TrafficFilters:        trInfluDataPatch.TrafficFilters,
		TrafficRoutes:         trInfluDataPatch.TrafficRoutes,
		ValidStartTime:        trInfluDataPatch.ValidStartTime,
		ValidEndTime:          trInfluDataPatch.ValidEndTime,
		NwAreaInfo:            trInfluDataPatch.NwAreaInfo,
		UpPathChgNotifUri:     trInfluDataPatch.UpPathChgNotifUri,
	}
	newData := util.ToBsonM(trInfluData)

	// Add "influenceId" entry to DB
	newData["influenceId"] = influID
	_, errPutOne := CommonDBClient.RestfulAPIPutOne(APPDATA_INFLUDATA_DB_COLLECTION_NAME, filter, newData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	// Roll back to origin data before return
	delete(newData, "influenceId")

	return newData, http.StatusOK
}

func HandleApplicationDataInfluenceDataInfluenceIdPut(influID string,
	trInfluData *models.TrafficInfluData,
) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataInfluenceDataInfluenceIdPut: influID=%q", influID)

	response, status := putApplicationDataIndividualInfluenceDataToDB(influID, trInfluData)

	return httpwrapper.NewResponse(status, nil, response)
}

func putApplicationDataIndividualInfluenceDataToDB(influID string,
	trInfluData *models.TrafficInfluData,
) (bson.M, int) {
	filter := bson.M{"influenceId": influID}
	data := util.ToBsonM(*trInfluData)

	// Add "influenceId" entry to DB
	data["influenceId"] = influID
	isExisted, errPutOne := CommonDBClient.RestfulAPIPutOne(APPDATA_INFLUDATA_DB_COLLECTION_NAME, filter, data)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	// Roll back to origin data before return
	delete(data, "influenceId")

	if isExisted {
		return data, http.StatusOK
	}
	return data, http.StatusCreated
}

func HandleApplicationDataInfluenceDataSubsToNotifyGet(queryParams map[string][]string) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle ApplicationDataInfluenceDataSubsToNotifyGet: queryParams=%#v", queryParams)

	dnn := queryParams["dnn"]
	snssai := queryParams["snssai"]
	intGroupID := queryParams["internal-Group-Id"]
	supi := queryParams["supi"]
	if len(dnn) == 0 && len(snssai) == 0 && len(intGroupID) == 0 && len(supi) == 0 {
		stats.IncrementUdrApplicationDataStats("get", "influence-data-notify", "FAILURE")
		pd := util.ProblemDetailsMalformedReqSyntax("No query parameters")
		return httpwrapper.NewResponse(int(pd.Status), nil, pd)
	}
	if len(dnn) > 1 {
		stats.IncrementUdrApplicationDataStats("get", "influence-data-notify", "FAILURE")
		pd := util.ProblemDetailsMalformedReqSyntax("Too many dnn query parameters")
		return httpwrapper.NewResponse(int(pd.Status), nil, pd)
	}
	if len(snssai) > 1 {
		stats.IncrementUdrApplicationDataStats("get", "influence-data-notify", "FAILURE")
		pd := util.ProblemDetailsMalformedReqSyntax("Too many snssai query parameters")
		return httpwrapper.NewResponse(int(pd.Status), nil, pd)
	}
	if len(intGroupID) > 1 {
		stats.IncrementUdrApplicationDataStats("get", "influence-data-notify", "FAILURE")
		pd := util.ProblemDetailsMalformedReqSyntax("Too many internal-Group-Id query parameters")
		return httpwrapper.NewResponse(int(pd.Status), nil, pd)
	}
	if len(supi) > 1 {
		stats.IncrementUdrApplicationDataStats("get", "influence-data-notify", "FAILURE")
		pd := util.ProblemDetailsMalformedReqSyntax("Too many supi query parameters")
		return httpwrapper.NewResponse(int(pd.Status), nil, pd)
	}

	response := getApplicationDataInfluenceDataSubsToNotifyfromDB(dnn, snssai, intGroupID, supi)
	stats.IncrementUdrApplicationDataStats("get", "influence-data-notify", "SUCCESS")

	return httpwrapper.NewResponse(http.StatusOK, nil, response)
}

func getApplicationDataInfluenceDataSubsToNotifyfromDB(dnn, snssai, intGroupID,
	supi []string,
) []map[string]interface{} {
	filter := bson.M{}
	if len(dnn) != 0 {
		filter["dnns"] = dnn[0]
	}
	if len(intGroupID) != 0 {
		filter["internalGroupIds"] = intGroupID[0]
	}
	if len(supi) != 0 {
		filter["supis"] = supi[0]
	}
	matchedSubs, errGetMany := CommonDBClient.RestfulAPIGetMany(APPDATA_INFLUDATA_SUBSC_DB_COLLECTION_NAME, filter)
	if errGetMany != nil {
		logger.DataRepoLog.Warnln(errGetMany)
	}
	if len(snssai) != 0 {
		matchedSubs = filterDataBySnssais(snssai[0], matchedSubs)
	}
	for i := 0; i < len(matchedSubs); i++ {
		// Delete "_id" entry which is auto-inserted by MongoDB
		delete(matchedSubs[i], "_id")
		// Delete "subscriptionId" entry which is added by us
		delete(matchedSubs[i], "subscriptionId")
	}
	return matchedSubs
}

func filterDataBySnssais(snssaiValue string,
	datas []map[string]interface{},
) []map[string]interface{} {
	var matchedDatas []map[string]interface{}
	var filterSnssai models.Snssai
	if err := json.Unmarshal([]byte(snssaiValue), &filterSnssai); err != nil {
		logger.DataRepoLog.Warnln(err)
	}
	logger.DataRepoLog.Debugf("filterSnssai=%#v", filterSnssai)
	for _, data := range datas {
		var dataSnssais []models.Snssai
		if err := json.Unmarshal(
			util.PrimitiveAToByte(data["snssais"].(primitive.A)), &dataSnssais); err != nil {
			logger.DataRepoLog.Warnln(err)
			break
		}
		logger.DataRepoLog.Debugf("dataSnssais=%#v", dataSnssais)
		for _, v := range dataSnssais {
			if v.Sd == filterSnssai.Sd && v.Sst == filterSnssai.Sst {
				matchedDatas = append(matchedDatas, data)
				break
			}
		}
	}
	return matchedDatas
}

func HandleApplicationDataInfluenceDataSubsToNotifyPost(trInfluSub *models.TrafficInfluSub) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle ApplicationDataInfluenceDataSubsToNotifyPost")
	udrSelf := udr_context.UDR_Self()

	newSubscID := strconv.FormatUint(udrSelf.NewAppDataInfluDataSubscriptionID(), 10)
	response, status := postApplicationDataInfluenceDataSubsToNotifyToDB(newSubscID, trInfluSub)

	/* Contains the URI of the newly created resource, according
	   to the structure: {apiRoot}/application-data/influenceData/subs-to-notify/{subscID} */
	locationHeader := fmt.Sprintf("%s/application-data/influenceData/subs-to-notify/%s",
		udrSelf.GetIPv4GroupUri(udr_context.NUDR_DR), newSubscID)
	logger.DataRepoLog.Infof("locationHeader:%q", locationHeader)
	headers := http.Header{}
	headers.Set("Location", locationHeader)
	return httpwrapper.NewResponse(status, headers, response)
}

func postApplicationDataInfluenceDataSubsToNotifyToDB(subscID string,
	trInfluSub *models.TrafficInfluSub,
) (bson.M, int) {
	filter := bson.M{"subscriptionId": subscID}
	data := util.ToBsonM(*trInfluSub)

	// Add "subscriptionId" entry to DB
	data["subscriptionId"] = subscID
	_, errPutOne := CommonDBClient.RestfulAPIPutOne(APPDATA_INFLUDATA_SUBSC_DB_COLLECTION_NAME, filter, data)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	// Revert back to origin data before return
	delete(data, "subscriptionId")
	return data, http.StatusCreated
}

func HandleApplicationDataInfluenceDataSubsToNotifySubscriptionIdDelete(subscID string) *httpwrapper.Response {
	logger.DataRepoLog.Infof(
		"handle ApplicationDataInfluenceDataSubsToNotifySubscriptionIdDelete: subscID=%q", subscID)

	err := deleteApplicationDataIndividualInfluenceDataSubsToNotifyFromDB(subscID)
	if err == nil {
		stats.IncrementUdrApplicationDataStats("delete", "influence-data-subscription", "SUCCESS")
	} else {
		stats.IncrementUdrApplicationDataStats("delete", "influence-data-subscription", "FAILURE")
	}

	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func deleteApplicationDataIndividualInfluenceDataSubsToNotifyFromDB(subscID string) error {
	filter := bson.M{"subscriptionId": subscID}
	return deleteDataFromDB(APPDATA_INFLUDATA_SUBSC_DB_COLLECTION_NAME, filter)
}

func HandleApplicationDataInfluenceDataSubsToNotifySubscriptionIdGet(subscID string) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataInfluenceDataSubsToNotifySubscriptionIdGet: subscID=%s", subscID)

	response, problemDetails := getApplicationDataIndividualInfluenceDataSubsToNotifyFromDB(subscID)

	if problemDetails != nil {
		stats.IncrementUdrApplicationDataStats("get", "influence-data-subscription", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
	stats.IncrementUdrApplicationDataStats("get", "influence-data-subscription", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusOK, nil, response)
}

func getApplicationDataIndividualInfluenceDataSubsToNotifyFromDB(
	subscID string,
) (map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"subscriptionId": subscID}
	data, problemDetails := getDataFromDB(APPDATA_INFLUDATA_SUBSC_DB_COLLECTION_NAME, filter)
	if data != nil {
		// Delete "subscriptionId" entry which is added by us
		delete(data, "subscriptionId")
	}
	return data, problemDetails
}

func HandleApplicationDataInfluenceDataSubsToNotifySubscriptionIdPut(
	subscID string, trInfluSub *models.TrafficInfluSub,
) *httpwrapper.Response {
	logger.DataRepoLog.Infof(
		"handle HandleApplicationDataInfluenceDataSubsToNotifySubscriptionIdPut: subscID=%q", subscID)

	response, status := putApplicationDataIndividualInfluenceDataSubsToNotifyToDB(subscID, trInfluSub)
	if response != nil {
		stats.IncrementUdrApplicationDataStats("update", "influence-data-subscription", "SUCCESS")
	} else {
		stats.IncrementUdrApplicationDataStats("update", "influence-data-subscription", "FAILURE")
	}

	return httpwrapper.NewResponse(status, nil, response)
}

func putApplicationDataIndividualInfluenceDataSubsToNotifyToDB(subscID string,
	trInfluSub *models.TrafficInfluSub,
) (bson.M, int) {
	filter := bson.M{"subscriptionId": subscID}
	newData := util.ToBsonM(*trInfluSub)

	oldData, errGetOne := CommonDBClient.RestfulAPIGetOne(APPDATA_INFLUDATA_SUBSC_DB_COLLECTION_NAME, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}
	if oldData == nil {
		return nil, http.StatusNotFound
	}
	// Add "subscriptionId" entry to DB
	newData["subscriptionId"] = subscID
	// Modify with new data
	_, errPutOne := CommonDBClient.RestfulAPIPutOne(APPDATA_INFLUDATA_SUBSC_DB_COLLECTION_NAME, filter, newData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	// Roll back to origin data before return
	delete(newData, "subscriptionId")
	return newData, http.StatusOK
}

func HandleApplicationDataPfdsAppIdDelete(appID string) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataPfdsAppIdDelete: appID=%s", appID)

	err := deleteApplicationDataIndividualPfdFromDB(appID)
	if err == nil {
		stats.IncrementUdrApplicationDataStats("delete", "pfds", "SUCCESS")
	} else {
		stats.IncrementUdrApplicationDataStats("delete", "pfds", "FAILURE")
	}
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func deleteApplicationDataIndividualPfdFromDB(appID string) error {
	filter := bson.M{"applicationId": appID}
	return deleteDataFromDB(APPDATA_PFD_DB_COLLECTION_NAME, filter)
}

func HandleApplicationDataPfdsAppIdGet(appID string) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataPfdsAppIdGet: appID=%s", appID)

	response, problemDetails := getApplicationDataIndividualPfdFromDB(appID)

	if problemDetails != nil {
		stats.IncrementUdrApplicationDataStats("get", "pfds", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
	stats.IncrementUdrApplicationDataStats("get", "pfds", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusOK, nil, response)
}

func getApplicationDataIndividualPfdFromDB(appID string) (map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"applicationId": appID}
	return getDataFromDB(APPDATA_PFD_DB_COLLECTION_NAME, filter)
}

func HandleApplicationDataPfdsAppIdPut(appID string, pfdDataForApp *models.PfdDataForApp) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataPfdsAppIdPut: appID=%s", appID)

	response, status := putApplicationDataIndividualPfdToDB(appID, pfdDataForApp)
	if response != nil {
		stats.IncrementUdrApplicationDataStats("update", "pfds", "SUCCESS")
	} else {
		stats.IncrementUdrApplicationDataStats("update", "pfds", "FAILURE")
	}
	return httpwrapper.NewResponse(status, nil, response)
}

func putApplicationDataIndividualPfdToDB(appID string, pfdDataForApp *models.PfdDataForApp) (bson.M, int) {
	filter := bson.M{"applicationId": appID}
	data := util.ToBsonM(*pfdDataForApp)

	isExisted, errPutOne := CommonDBClient.RestfulAPIPutOne(APPDATA_PFD_DB_COLLECTION_NAME, filter, data)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}

	if isExisted {
		return data, http.StatusOK
	}
	return data, http.StatusCreated
}

func HandleApplicationDataPfdsGet(pfdsAppIDs []string) *httpwrapper.Response {
	logger.DataRepoLog.Infof("handle ApplicationDataPfdsGet: pfdsAppIDs=%#v", pfdsAppIDs)

	// TODO: Parse appID with separator ','
	// Ex: "app1,app2,..."
	response := getApplicationDataPfdsFromDB(pfdsAppIDs)
	stats.IncrementUdrApplicationDataStats("get", "pfds", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusOK, nil, response)
}

func getApplicationDataPfdsFromDB(pfdsAppIDs []string) (response []map[string]interface{}) {
	filter := bson.M{}

	var matchedPfds []map[string]interface{}
	var errGetMany error
	if len(pfdsAppIDs) == 0 {
		matchedPfds, errGetMany = CommonDBClient.RestfulAPIGetMany(APPDATA_PFD_DB_COLLECTION_NAME, filter)
		if errGetMany != nil {
			logger.DataRepoLog.Warnln(errGetMany)
		}
		for i := 0; i < len(matchedPfds); i++ {
			delete(matchedPfds[i], "_id")
		}
	} else {
		for _, v := range pfdsAppIDs {
			filter := bson.M{"applicationId": v}
			data, errGetOne := CommonDBClient.RestfulAPIGetOne(APPDATA_PFD_DB_COLLECTION_NAME, filter)
			if errGetOne != nil {
				logger.DataRepoLog.Warnln(errGetOne)
			}
			if data != nil {
				// Delete "_id" entry which is auto-inserted by MongoDB
				delete(data, "_id")
				matchedPfds = append(matchedPfds, data)
			}
		}
	}
	return matchedPfds
}

func HandleExposureDataSubsToNotifyPost(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandleExposureDataSubsToNotifySubIdDelete(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandleExposureDataSubsToNotifySubIdPut(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandlePolicyDataBdtDataBdtReferenceIdDelete(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataBdtDataBdtReferenceIdDelete")

	collName := POLICYDATA_BDTDATA
	bdtReferenceId := request.Params["bdtReferenceId"]

	err := PolicyDataBdtDataBdtReferenceIdDeleteProcedure(collName, bdtReferenceId)
	if err == nil {
		stats.IncrementUdrPolicyDataStats("delete", "bdt-data", "SUCCESS")
	} else {
		stats.IncrementUdrPolicyDataStats("delete", "bdt-data", "FAILURE")
	}
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func PolicyDataBdtDataBdtReferenceIdDeleteProcedure(collName string, bdtReferenceId string) error {
	filter := bson.M{"bdtReferenceId": bdtReferenceId}
	errDelOne := CommonDBClient.RestfulAPIDeleteOne(collName, filter)
	if errDelOne != nil {
		logger.DataRepoLog.Warnln(errDelOne)
	}
	return errDelOne
}

func HandlePolicyDataBdtDataBdtReferenceIdGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataBdtDataBdtReferenceIdGet")

	collName := POLICYDATA_BDTDATA
	bdtReferenceId := request.Params["bdtReferenceId"]

	response, problemDetails := PolicyDataBdtDataBdtReferenceIdGetProcedure(collName, bdtReferenceId)
	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "bdt-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrPolicyDataStats("get", "bdt-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "bdt-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataBdtDataBdtReferenceIdGetProcedure(collName string, bdtReferenceId string) (*map[string]interface{},
	*models.ProblemDetails,
) {
	filter := bson.M{"bdtReferenceId": bdtReferenceId}

	bdtData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if bdtData != nil {
		return &bdtData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("DATA_NOT_FOUND")
	}
}

func HandlePolicyDataBdtDataBdtReferenceIdPut(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataBdtDataBdtReferenceIdPut")

	collName := POLICYDATA_BDTDATA
	bdtReferenceId := request.Params["bdtReferenceId"]
	bdtData := request.Body.(models.BdtData)

	response := PolicyDataBdtDataBdtReferenceIdPutProcedure(collName, bdtReferenceId, bdtData)
	if response != nil {
		stats.IncrementUdrPolicyDataStats("update", "bdt-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("update", "bdt-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataBdtDataBdtReferenceIdPutProcedure(collName string, bdtReferenceId string,
	bdtData models.BdtData,
) bson.M {
	putData := util.ToBsonM(bdtData)
	putData["bdtReferenceId"] = bdtReferenceId
	filter := bson.M{"bdtReferenceId": bdtReferenceId}

	isExisted, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}

	if isExisted {
		PreHandlePolicyDataChangeNotification("", bdtReferenceId, bdtData)
		return putData
	} else {
		return putData
	}
}

func HandlePolicyDataBdtDataGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataBdtDataGet")

	collName := POLICYDATA_BDTDATA

	response := PolicyDataBdtDataGetProcedure(collName)
	stats.IncrementUdrPolicyDataStats("get", "bdt-data", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusOK, nil, response)
}

func PolicyDataBdtDataGetProcedure(collName string) (response *[]map[string]interface{}) {
	filter := bson.M{}
	bdtDataArray, errGetMany := CommonDBClient.RestfulAPIGetMany(collName, filter)
	if errGetMany != nil {
		logger.DataRepoLog.Warnln(errGetMany)
	}
	return &bdtDataArray
}

func HandlePolicyDataPlmnsPlmnIdUePolicySetGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataPlmnsPlmnIdUePolicySetGet")

	collName := "policyData.plmns.uePolicySet"
	plmnId := request.Params["plmnId"]

	response, problemDetails := PolicyDataPlmnsPlmnIdUePolicySetGetProcedure(collName, plmnId)

	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "plmn-ue-policy-set", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrPolicyDataStats("get", "plmn-ue-policy-set", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "plmn-ue-policy-set", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataPlmnsPlmnIdUePolicySetGetProcedure(collName string,
	plmnId string,
) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"plmnId": plmnId}
	uePolicySet, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if uePolicySet != nil {
		return &uePolicySet, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandlePolicyDataSponsorConnectivityDataSponsorIdGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataSponsorConnectivityDataSponsorIdGet")

	collName := "policyData.sponsorConnectivityData"
	sponsorId := request.Params["sponsorId"]

	response, status := PolicyDataSponsorConnectivityDataSponsorIdGetProcedure(collName, sponsorId)

	switch status {
	case http.StatusOK:
		stats.IncrementUdrPolicyDataStats("get", "sponsor-connectivity-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	case http.StatusNoContent:
		stats.IncrementUdrPolicyDataStats("get", "sponsor-connectivity-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "sponsor-connectivity-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataSponsorConnectivityDataSponsorIdGetProcedure(collName string,
	sponsorId string,
) (*map[string]interface{}, int) {
	filter := bson.M{"sponsorId": sponsorId}

	sponsorConnectivityData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if sponsorConnectivityData != nil {
		return &sponsorConnectivityData, http.StatusOK
	} else {
		return nil, http.StatusNoContent
	}
}

func HandlePolicyDataSubsToNotifyPost(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataSubsToNotifyPost")

	PolicyDataSubscription := request.Body.(models.PolicyDataSubscription)

	locationHeader := PolicyDataSubsToNotifyPostProcedure(PolicyDataSubscription)

	headers := http.Header{}
	headers.Set("Location", locationHeader)
	stats.IncrementUdrPolicyDataStats("create", "subs-to-notify", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusCreated, headers, PolicyDataSubscription)
}

func PolicyDataSubsToNotifyPostProcedure(PolicyDataSubscription models.PolicyDataSubscription) string {
	udrSelf := udr_context.UDR_Self()

	newSubscriptionID := strconv.Itoa(udrSelf.PolicyDataSubscriptionIDGenerator)
	udrSelf.PolicyDataSubscriptions[newSubscriptionID] = &PolicyDataSubscription
	udrSelf.PolicyDataSubscriptionIDGenerator++

	/* Contains the URI of the newly created resource, according
	   to the structure: {apiRoot}/subscription-data/subs-to-notify/{subsId} */
	locationHeader := fmt.Sprintf("%s/policy-data/subs-to-notify/%s", udrSelf.GetIPv4GroupUri(udr_context.NUDR_DR),
		newSubscriptionID)

	return locationHeader
}

func HandlePolicyDataSubsToNotifySubsIdDelete(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataSubsToNotifySubsIdDelete")

	subsId := request.Params["subsId"]

	problemDetails := PolicyDataSubsToNotifySubsIdDeleteProcedure(subsId)

	if problemDetails == nil {
		stats.IncrementUdrPolicyDataStats("delete", "subs-to-notify", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrPolicyDataStats("delete", "subs-to-notify", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func PolicyDataSubsToNotifySubsIdDeleteProcedure(subsId string) (problemDetails *models.ProblemDetails) {
	udrSelf := udr_context.UDR_Self()
	_, ok := udrSelf.PolicyDataSubscriptions[subsId]
	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	delete(udrSelf.PolicyDataSubscriptions, subsId)

	return nil
}

func HandlePolicyDataSubsToNotifySubsIdPut(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataSubsToNotifySubsIdPut")

	subsId := request.Params["subsId"]
	policyDataSubscription := request.Body.(models.PolicyDataSubscription)

	response, problemDetails := PolicyDataSubsToNotifySubsIdPutProcedure(subsId, policyDataSubscription)

	if problemDetails == nil {
		stats.IncrementUdrPolicyDataStats("update", "subs-to-notify", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else {
		stats.IncrementUdrPolicyDataStats("update", "subs-to-notify", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func PolicyDataSubsToNotifySubsIdPutProcedure(subsId string,
	policyDataSubscription models.PolicyDataSubscription,
) (*models.PolicyDataSubscription, *models.ProblemDetails) {
	udrSelf := udr_context.UDR_Self()
	_, ok := udrSelf.PolicyDataSubscriptions[subsId]
	if !ok {
		return nil, util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}

	udrSelf.PolicyDataSubscriptions[subsId] = &policyDataSubscription

	return &policyDataSubscription, nil
}

func HandlePolicyDataUesUeIdAmDataGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdAmDataGet")

	collName := "policyData.ues.amData"
	ueId := request.Params["ueId"]

	response, problemDetails := PolicyDataUesUeIdAmDataGetProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "am-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrPolicyDataStats("get", "am-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "am-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataUesUeIdAmDataGetProcedure(collName string,
	ueId string,
) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	amPolicyData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if amPolicyData != nil {
		return &amPolicyData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandlePolicyDataUesUeIdOperatorSpecificDataGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdOperatorSpecificDataGet")

	collName := POLICYDATA_UES_OPSPECDATA
	ueId := request.Params["ueId"]

	response, problemDetails := PolicyDataUesUeIdOperatorSpecificDataGetProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "operator-specific-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrPolicyDataStats("get", "operator-specific-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "operator-specific-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataUesUeIdOperatorSpecificDataGetProcedure(collName string,
	ueId string,
) (*interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	operatorSpecificDataContainerMapCover, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if operatorSpecificDataContainerMapCover != nil {
		operatorSpecificDataContainerMap := operatorSpecificDataContainerMapCover["operatorSpecificDataContainerMap"]
		return &operatorSpecificDataContainerMap, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandlePolicyDataUesUeIdOperatorSpecificDataPatch(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdOperatorSpecificDataPatch")

	collName := POLICYDATA_UES_OPSPECDATA
	ueId := request.Params["ueId"]
	patchItem := request.Body.([]models.PatchItem)

	problemDetails := PolicyDataUesUeIdOperatorSpecificDataPatchProcedure(collName, ueId, patchItem)

	if problemDetails == nil {
		stats.IncrementUdrPolicyDataStats("update", "operator-specific-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrPolicyDataStats("update", "operator-specific-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func PolicyDataUesUeIdOperatorSpecificDataPatchProcedure(collName string, ueId string,
	patchItem []models.PatchItem,
) *models.ProblemDetails {
	filter := bson.M{"ueId": ueId}

	patchJSON, err := json.Marshal(patchItem)
	if err != nil {
		logger.DataRepoLog.Warnln(err)
	}

	failure := CommonDBClient.RestfulAPIJSONPatchExtend(collName, filter, patchJSON,
		"operatorSpecificDataContainerMap")

	if failure == nil {
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandlePolicyDataUesUeIdOperatorSpecificDataPut(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdOperatorSpecificDataPut")

	// json.NewDecoder(c.Request.Body).Decode(&operatorSpecificDataContainerMap)

	collName := POLICYDATA_UES_OPSPECDATA
	ueId := request.Params["ueId"]
	OperatorSpecificDataContainer := request.Body.(map[string]models.OperatorSpecificDataContainer)

	err := PolicyDataUesUeIdOperatorSpecificDataPutProcedure(collName, ueId, OperatorSpecificDataContainer)
	if err == nil {
		stats.IncrementUdrPolicyDataStats("create", "operator-specific-data", "SUCCESS")
	} else {
		stats.IncrementUdrPolicyDataStats("create", "operator-specific-data", "FAILURE")
	}

	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func PolicyDataUesUeIdOperatorSpecificDataPutProcedure(collName string, ueId string,
	OperatorSpecificDataContainer map[string]models.OperatorSpecificDataContainer,
) error {
	filter := bson.M{"ueId": ueId}

	putData := map[string]interface{}{"operatorSpecificDataContainerMap": OperatorSpecificDataContainer}
	putData["ueId"] = ueId

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	return errPutOne
}

func HandlePolicyDataUesUeIdSmDataGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdSmDataGet")

	collName := "policyData.ues.smData"
	ueId := request.Params["ueId"]
	sNssai := models.Snssai{}
	sNssaiQuery := request.Query.Get("snssai")
	err := json.Unmarshal([]byte(sNssaiQuery), &sNssai)
	if err != nil {
		logger.DataRepoLog.Warnln(err)
	}
	dnn := request.Query.Get("dnn")

	response, problemDetails := PolicyDataUesUeIdSmDataGetProcedure(collName, ueId, sNssai, dnn)
	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "sm-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrPolicyDataStats("get", "sm-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "sm-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataUesUeIdSmDataGetProcedure(collName string, ueId string, snssai models.Snssai,
	dnn string,
) (*models.SmPolicyData, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	if !reflect.DeepEqual(snssai, models.Snssai{}) {
		filter["smPolicySnssaiData."+util.SnssaiModelsToHex(snssai)] = bson.M{"$exists": true}
	}
	if !reflect.DeepEqual(snssai, models.Snssai{}) && dnn != "" {
		filter["smPolicySnssaiData."+util.SnssaiModelsToHex(snssai)+".smPolicyDnnData."+dnn] = bson.M{"$exists": true}
	}

	smPolicyData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}
	if smPolicyData != nil {
		var smPolicyDataResp models.SmPolicyData
		err := json.Unmarshal(util.MapToByte(smPolicyData), &smPolicyDataResp)
		if err != nil {
			logger.DataRepoLog.Warnln(err)
		}
		{
			collName := POLICYDATA_UES_SMDATA_USAGEMONDATA
			filter := bson.M{"ueId": ueId}
			usageMonDataMapArray, errGetMany := CommonDBClient.RestfulAPIGetMany(collName, filter)
			if errGetMany != nil {
				logger.DataRepoLog.Warnln(errGetMany)
			}

			if !reflect.DeepEqual(usageMonDataMapArray, []map[string]interface{}{}) {
				var usageMonDataArray []models.UsageMonData
				err = json.Unmarshal(util.MapArrayToByte(usageMonDataMapArray), &usageMonDataArray)
				if err != nil {
					logger.DataRepoLog.Warnln(err)
				}
				smPolicyDataResp.UmData = make(map[string]models.UsageMonData)
				for _, element := range usageMonDataArray {
					smPolicyDataResp.UmData[element.LimitId] = element
				}
			}
		}
		return &smPolicyDataResp, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandlePolicyDataUesUeIdSmDataPatch(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdSmDataPatch")

	collName := POLICYDATA_UES_SMDATA_USAGEMONDATA
	ueId := request.Params["ueId"]
	usageMonData := request.Body.(map[string]models.UsageMonData)

	problemDetails := PolicyDataUesUeIdSmDataPatchProcedure(collName, ueId, usageMonData)
	if problemDetails == nil {
		stats.IncrementUdrPolicyDataStats("update", "sm-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrPolicyDataStats("update", "sm-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func PolicyDataUesUeIdSmDataPatchProcedure(collName string, ueId string,
	UsageMonData map[string]models.UsageMonData,
) *models.ProblemDetails {
	filter := bson.M{"ueId": ueId}

	successAll := true
	for k, usageMonData := range UsageMonData {
		limitId := k
		filterTmp := bson.M{"ueId": ueId, "limitId": limitId}
		failure := CommonDBClient.RestfulAPIMergePatch(collName, filterTmp, util.ToBsonM(usageMonData))
		if failure != nil {
			successAll = false
		} else {
			var usageMonData models.UsageMonData
			usageMonDataBsonM, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
			if errGetOne != nil {
				logger.DataRepoLog.Warnln(errGetOne)
			}
			err := json.Unmarshal(util.MapToByte(usageMonDataBsonM), &usageMonData)
			if err != nil {
				logger.DataRepoLog.Warnln(err)
			}
			PreHandlePolicyDataChangeNotification(ueId, limitId, usageMonData)
		}
	}

	if successAll {
		smPolicyDataBsonM, errGetOneNew := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOneNew != nil {
			logger.DataRepoLog.Warnln(errGetOneNew)
		}
		var smPolicyData models.SmPolicyData
		err := json.Unmarshal(util.MapToByte(smPolicyDataBsonM), &smPolicyData)
		if err != nil {
			logger.DataRepoLog.Warnln(err)
		}
		{
			collName := POLICYDATA_UES_SMDATA_USAGEMONDATA
			filter := bson.M{"ueId": ueId}
			usageMonDataMapArray, errGetMany := CommonDBClient.RestfulAPIGetMany(collName, filter)
			if errGetMany != nil {
				logger.DataRepoLog.Warnln(errGetMany)
			}

			if !reflect.DeepEqual(usageMonDataMapArray, []map[string]interface{}{}) {
				var usageMonDataArray []models.UsageMonData
				err = json.Unmarshal(util.MapArrayToByte(usageMonDataMapArray), &usageMonDataArray)
				if err != nil {
					logger.DataRepoLog.Warnln(err)
				}
				smPolicyData.UmData = make(map[string]models.UsageMonData)
				for _, element := range usageMonDataArray {
					smPolicyData.UmData[element.LimitId] = element
				}
			}
		}
		PreHandlePolicyDataChangeNotification(ueId, "", smPolicyData)
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandlePolicyDataUesUeIdSmDataUsageMonIdDelete(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdSmDataUsageMonIdDelete")

	collName := POLICYDATA_UES_SMDATA_USAGEMONDATA
	ueId := request.Params["ueId"]
	usageMonId := request.Params["usageMonId"]

	err := PolicyDataUesUeIdSmDataUsageMonIdDeleteProcedure(collName, ueId, usageMonId)
	if err == nil {
		stats.IncrementUdrPolicyDataStats("delete", "sm-data", "SUCCESS")
	} else {
		stats.IncrementUdrPolicyDataStats("delete", "sm-data", "FAILURE")
	}
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func PolicyDataUesUeIdSmDataUsageMonIdDeleteProcedure(collName string, ueId string, usageMonId string) error {
	filter := bson.M{"ueId": ueId, "usageMonId": usageMonId}
	errDelOne := CommonDBClient.RestfulAPIDeleteOne(collName, filter)
	if errDelOne != nil {
		logger.DataRepoLog.Warnln(errDelOne)
	}
	return errDelOne
}

func HandlePolicyDataUesUeIdSmDataUsageMonIdGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdSmDataUsageMonIdGet")

	collName := POLICYDATA_UES_SMDATA_USAGEMONDATA
	ueId := request.Params["ueId"]
	usageMonId := request.Params["usageMonId"]

	response := PolicyDataUesUeIdSmDataUsageMonIdGetProcedure(collName, usageMonId, ueId)

	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "sm-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else {
		stats.IncrementUdrPolicyDataStats("get", "sm-data", "FAILURE")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	}
}

func PolicyDataUesUeIdSmDataUsageMonIdGetProcedure(collName string, usageMonId string,
	ueId string,
) *map[string]interface{} {
	filter := bson.M{"ueId": ueId, "usageMonId": usageMonId}

	usageMonData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	return &usageMonData
}

func HandlePolicyDataUesUeIdSmDataUsageMonIdPut(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdSmDataUsageMonIdPut")

	ueId := request.Params["ueId"]
	usageMonId := request.Params["usageMonId"]
	usageMonData := request.Body.(models.UsageMonData)
	collName := POLICYDATA_UES_SMDATA_USAGEMONDATA

	response := PolicyDataUesUeIdSmDataUsageMonIdPutProcedure(collName, ueId, usageMonId, usageMonData)
	stats.IncrementUdrPolicyDataStats("create", "sm-data", "SUCCESS")

	return httpwrapper.NewResponse(http.StatusCreated, nil, response)
}

func PolicyDataUesUeIdSmDataUsageMonIdPutProcedure(collName string, ueId string, usageMonId string,
	usageMonData models.UsageMonData,
) *bson.M {
	putData := util.ToBsonM(usageMonData)
	putData["ueId"] = ueId
	putData["usageMonId"] = usageMonId
	filter := bson.M{"ueId": ueId, "usageMonId": usageMonId}

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	return &putData
}

func HandlePolicyDataUesUeIdUePolicySetGet(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdUePolicySetGet")

	ueId := request.Params["ueId"]
	collName := POLICYDATA_UES_UEPOLICYSET

	response, problemDetails := PolicyDataUesUeIdUePolicySetGetProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "ue-policy-set", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrPolicyDataStats("get", "ue-policy-set", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "ue-policy-set", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataUesUeIdUePolicySetGetProcedure(collName string, ueId string) (*map[string]interface{},
	*models.ProblemDetails,
) {
	filter := bson.M{"ueId": ueId}

	uePolicySet, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if uePolicySet != nil {
		return &uePolicySet, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandlePolicyDataUesUeIdUePolicySetPatch(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdUePolicySetPatch")

	collName := POLICYDATA_UES_UEPOLICYSET
	ueId := request.Params["ueId"]
	UePolicySet := request.Body.(models.UePolicySet)

	problemDetails := PolicyDataUesUeIdUePolicySetPatchProcedure(collName, ueId, UePolicySet)

	if problemDetails == nil {
		stats.IncrementUdrPolicyDataStats("update", "ue-policy-set", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrPolicyDataStats("update", "ue-policy-set", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func PolicyDataUesUeIdUePolicySetPatchProcedure(collName string, ueId string,
	UePolicySet models.UePolicySet,
) *models.ProblemDetails {
	patchData := util.ToBsonM(UePolicySet)
	patchData["ueId"] = ueId
	filter := bson.M{"ueId": ueId}

	failure := CommonDBClient.RestfulAPIMergePatch(collName, filter, patchData)

	if failure == nil {
		var uePolicySet models.UePolicySet
		uePolicySetBsonM, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Warnln(errGetOne)
		}
		err := json.Unmarshal(util.MapToByte(uePolicySetBsonM), &uePolicySet)
		if err != nil {
			logger.DataRepoLog.Warnln(err)
		}
		PreHandlePolicyDataChangeNotification(ueId, "", uePolicySet)
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandlePolicyDataUesUeIdUePolicySetPut(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PolicyDataUesUeIdUePolicySetPut")

	collName := POLICYDATA_UES_UEPOLICYSET
	ueId := request.Params["ueId"]
	UePolicySet := request.Body.(models.UePolicySet)

	response, status := PolicyDataUesUeIdUePolicySetPutProcedure(collName, ueId, UePolicySet)

	switch status {
	case http.StatusNoContent:
		stats.IncrementUdrPolicyDataStats("create", "ue-policy-set", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	case http.StatusCreated:
		stats.IncrementUdrPolicyDataStats("create", "ue-policy-set", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusCreated, nil, response)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("create", "ue-policy-set", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func PolicyDataUesUeIdUePolicySetPutProcedure(collName string, ueId string,
	UePolicySet models.UePolicySet,
) (bson.M, int) {
	putData := util.ToBsonM(UePolicySet)
	putData["ueId"] = ueId
	filter := bson.M{"ueId": ueId}

	isExisted, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
	if !isExisted {
		return putData, http.StatusCreated
	} else {
		return nil, http.StatusNoContent
	}
}

func HandleCreateAMFSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateAMFSubscriptions")

	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]
	AmfSubscriptionInfo := request.Body.([]models.AmfSubscriptionInfo)

	problemDetails := CreateAMFSubscriptionsProcedure(subsId, ueId, AmfSubscriptionInfo)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("create", "amf-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("create", "amf-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func CreateAMFSubscriptionsProcedure(subsId string, ueId string,
	AmfSubscriptionInfo []models.AmfSubscriptionInfo,
) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
	UESubsData := value.(*udr_context.UESubsData)

	_, ok = UESubsData.EeSubscriptionCollection[subsId]
	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}

	UESubsData.EeSubscriptionCollection[subsId].AmfSubscriptionInfos = AmfSubscriptionInfo
	return nil
}

func HandleRemoveAmfSubscriptionsInfo(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle RemoveAmfSubscriptionsInfo")

	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]

	problemDetails := RemoveAmfSubscriptionsInfoProcedure(subsId, ueId)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("delete", "amf-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("delete", "amf-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func RemoveAmfSubscriptionsInfoProcedure(subsId string, ueId string) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	_, ok = UESubsData.EeSubscriptionCollection[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}

	if UESubsData.EeSubscriptionCollection[subsId].AmfSubscriptionInfos == nil {
		return util.ProblemDetailsNotFound("AMFSUBSCRIPTION_NOT_FOUND")
	}

	UESubsData.EeSubscriptionCollection[subsId].AmfSubscriptionInfos = nil

	return nil
}

func HandleModifyAmfSubscriptionInfo(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle ModifyAmfSubscriptionInfo")

	patchItem := request.Body.([]models.PatchItem)
	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]

	problemDetails := ModifyAmfSubscriptionInfoProcedure(ueId, subsId, patchItem)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "amf-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "amf-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func ModifyAmfSubscriptionInfoProcedure(ueId string, subsId string,
	patchItem []models.PatchItem,
) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
	UESubsData := value.(*udr_context.UESubsData)

	_, ok = UESubsData.EeSubscriptionCollection[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}

	if UESubsData.EeSubscriptionCollection[subsId].AmfSubscriptionInfos == nil {
		return util.ProblemDetailsNotFound("AMFSUBSCRIPTION_NOT_FOUND")
	}
	var patchJSON []byte
	if patchJSONtemp, err := json.Marshal(patchItem); err != nil {
		logger.DataRepoLog.Errorln(err)
	} else {
		patchJSON = patchJSONtemp
	}
	var patch jsonpatch.Patch
	if patchtemp, err := jsonpatch.DecodePatch(patchJSON); err != nil {
		logger.DataRepoLog.Errorln(err)
		return util.ProblemDetailsModifyNotAllowed("PatchItem attributes are invalid")
	} else {
		patch = patchtemp
	}
	original, err := json.Marshal((UESubsData.EeSubscriptionCollection[subsId]).AmfSubscriptionInfos)
	if err != nil {
		logger.DataRepoLog.Warnln(err)
	}

	modified, err := patch.Apply(original)
	if err != nil {
		return util.ProblemDetailsModifyNotAllowed("Occur error when applying PatchItem")
	}
	var modifiedData []models.AmfSubscriptionInfo
	err = json.Unmarshal(modified, &modifiedData)
	if err != nil {
		logger.DataRepoLog.Error(err)
	}

	UESubsData.EeSubscriptionCollection[subsId].AmfSubscriptionInfos = modifiedData
	return nil
}

func HandleGetAmfSubscriptionInfo(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle GetAmfSubscriptionInfo")

	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]

	response, problemDetails := GetAmfSubscriptionInfoProcedure(subsId, ueId)
	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "amf-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "amf-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "amf-subscriptions", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func GetAmfSubscriptionInfoProcedure(subsId string, ueId string) (*[]models.AmfSubscriptionInfo,
	*models.ProblemDetails,
) {
	udrSelf := udr_context.UDR_Self()

	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	_, ok = UESubsData.EeSubscriptionCollection[subsId]

	if !ok {
		return nil, util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}

	if UESubsData.EeSubscriptionCollection[subsId].AmfSubscriptionInfos == nil {
		return nil, util.ProblemDetailsNotFound("AMFSUBSCRIPTION_NOT_FOUND")
	}
	return &UESubsData.EeSubscriptionCollection[subsId].AmfSubscriptionInfos, nil
}

func HandleQueryEEData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryEEData")

	ueId := request.Params["ueId"]
	collName := "subscriptionData.eeProfileData"

	response, problemDetails := QueryEEDataProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "ee-profile-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "ee-profile-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "ee-profile-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryEEDataProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}
	eeProfileData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if eeProfileData != nil {
		return &eeProfileData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleRemoveEeGroupSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle RemoveEeGroupSubscriptions")

	ueGroupId := request.Params["ueGroupId"]
	subsId := request.Params["subsId"]

	problemDetails := RemoveEeGroupSubscriptionsProcedure(ueGroupId, subsId)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("delete", "group-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("delete", "group-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func RemoveEeGroupSubscriptionsProcedure(ueGroupId string, subsId string) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UEGroupCollection.Load(ueGroupId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UEGroupSubsData := value.(*udr_context.UEGroupSubsData)
	_, ok = UEGroupSubsData.EeSubscriptions[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	delete(UEGroupSubsData.EeSubscriptions, subsId)

	return nil
}

func HandleUpdateEeGroupSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle UpdateEeGroupSubscriptions")

	ueGroupId := request.Params["ueGroupId"]
	subsId := request.Params["subsId"]
	EeSubscription := request.Body.(models.EeSubscription)

	problemDetails := UpdateEeGroupSubscriptionsProcedure(ueGroupId, subsId, EeSubscription)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "group-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "group-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func UpdateEeGroupSubscriptionsProcedure(ueGroupId string, subsId string,
	EeSubscription models.EeSubscription,
) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UEGroupCollection.Load(ueGroupId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UEGroupSubsData := value.(*udr_context.UEGroupSubsData)
	_, ok = UEGroupSubsData.EeSubscriptions[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	UEGroupSubsData.EeSubscriptions[subsId] = &EeSubscription

	return nil
}

func HandleCreateEeGroupSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateEeGroupSubscriptions")

	ueGroupId := request.Params["ueGroupId"]
	EeSubscription := request.Body.(models.EeSubscription)

	locationHeader := CreateEeGroupSubscriptionsProcedure(ueGroupId, EeSubscription)

	headers := http.Header{}
	headers.Set("Location", locationHeader)
	stats.IncrementUdrSubscriptionDataStats("create", "group-data", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusCreated, headers, EeSubscription)
}

func CreateEeGroupSubscriptionsProcedure(ueGroupId string, EeSubscription models.EeSubscription) string {
	udrSelf := udr_context.UDR_Self()

	value, ok := udrSelf.UEGroupCollection.Load(ueGroupId)
	if !ok {
		udrSelf.UEGroupCollection.Store(ueGroupId, new(udr_context.UEGroupSubsData))
		value, _ = udrSelf.UEGroupCollection.Load(ueGroupId)
	}
	UEGroupSubsData := value.(*udr_context.UEGroupSubsData)
	if UEGroupSubsData.EeSubscriptions == nil {
		UEGroupSubsData.EeSubscriptions = make(map[string]*models.EeSubscription)
	}

	newSubscriptionID := strconv.Itoa(udrSelf.EeSubscriptionIDGenerator)
	UEGroupSubsData.EeSubscriptions[newSubscriptionID] = &EeSubscription
	udrSelf.EeSubscriptionIDGenerator++

	/* Contains the URI of the newly created resource, according
	   to the structure: {apiRoot}/nudr-dr/v1/subscription-data/group-data/{ueGroupId}/ee-subscriptions */
	locationHeader := fmt.Sprintf("%s/nudr-dr/v1/subscription-data/group-data/%s/ee-subscriptions/%s",
		udrSelf.GetIPv4GroupUri(udr_context.NUDR_DR), ueGroupId, newSubscriptionID)

	return locationHeader
}

func HandleQueryEeGroupSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryEeGroupSubscriptions")

	ueGroupId := request.Params["ueGroupId"]

	response, problemDetails := QueryEeGroupSubscriptionsProcedure(ueGroupId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "group-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "group-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "group-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryEeGroupSubscriptionsProcedure(ueGroupId string) ([]models.EeSubscription, *models.ProblemDetails) {
	udrSelf := udr_context.UDR_Self()

	value, ok := udrSelf.UEGroupCollection.Load(ueGroupId)
	if !ok {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UEGroupSubsData := value.(*udr_context.UEGroupSubsData)
	var eeSubscriptionSlice []models.EeSubscription

	for _, v := range UEGroupSubsData.EeSubscriptions {
		eeSubscriptionSlice = append(eeSubscriptionSlice, *v)
	}
	return eeSubscriptionSlice, nil
}

func HandleRemoveeeSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle RemoveeeSubscriptions")

	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]

	problemDetails := RemoveeeSubscriptionsProcedure(ueId, subsId)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("delete", "ee-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("delete", "ee-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func RemoveeeSubscriptionsProcedure(ueId string, subsId string) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	_, ok = UESubsData.EeSubscriptionCollection[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	delete(UESubsData.EeSubscriptionCollection, subsId)
	return nil
}

func HandleUpdateEesubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle UpdateEesubscriptions")

	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]
	EeSubscription := request.Body.(models.EeSubscription)

	problemDetails := UpdateEesubscriptionsProcedure(ueId, subsId, EeSubscription)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "ee-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "ee-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func UpdateEesubscriptionsProcedure(ueId string, subsId string,
	EeSubscription models.EeSubscription,
) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	_, ok = UESubsData.EeSubscriptionCollection[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	UESubsData.EeSubscriptionCollection[subsId].EeSubscriptions = &EeSubscription

	return nil
}

func HandleCreateEeSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateEeSubscriptions")

	ueId := request.Params["ueId"]
	EeSubscription := request.Body.(models.EeSubscription)

	locationHeader := CreateEeSubscriptionsProcedure(ueId, EeSubscription)

	headers := http.Header{}
	headers.Set("Location", locationHeader)
	stats.IncrementUdrSubscriptionDataStats("create", "ee-subscriptions", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusCreated, headers, EeSubscription)
}

func CreateEeSubscriptionsProcedure(ueId string, EeSubscription models.EeSubscription) string {
	udrSelf := udr_context.UDR_Self()

	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		udrSelf.UESubsCollection.Store(ueId, new(udr_context.UESubsData))
		value, _ = udrSelf.UESubsCollection.Load(ueId)
	}
	UESubsData := value.(*udr_context.UESubsData)
	if UESubsData.EeSubscriptionCollection == nil {
		UESubsData.EeSubscriptionCollection = make(map[string]*udr_context.EeSubscriptionCollection)
	}

	newSubscriptionID := strconv.Itoa(udrSelf.EeSubscriptionIDGenerator)
	UESubsData.EeSubscriptionCollection[newSubscriptionID] = new(udr_context.EeSubscriptionCollection)
	UESubsData.EeSubscriptionCollection[newSubscriptionID].EeSubscriptions = &EeSubscription
	udrSelf.EeSubscriptionIDGenerator++

	/* Contains the URI of the newly created resource, according
	   to the structure: {apiRoot}/subscription-data/{ueId}/context-data/ee-subscriptions/{subsId} */
	locationHeader := fmt.Sprintf("%s/subscription-data/%s/context-data/ee-subscriptions/%s",
		udrSelf.GetIPv4GroupUri(udr_context.NUDR_DR), ueId, newSubscriptionID)

	return locationHeader
}

func HandleQueryeesubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle Queryeesubscriptions")

	ueId := request.Params["ueId"]

	response, problemDetails := QueryeesubscriptionsProcedure(ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "ee-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "ee-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "ee-subscriptions", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryeesubscriptionsProcedure(ueId string) ([]models.EeSubscription, *models.ProblemDetails) {
	udrSelf := udr_context.UDR_Self()

	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	var eeSubscriptionSlice []models.EeSubscription

	for _, v := range UESubsData.EeSubscriptionCollection {
		eeSubscriptionSlice = append(eeSubscriptionSlice, *v.EeSubscriptions)
	}
	return eeSubscriptionSlice, nil
}

func HandlePatchOperSpecData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PatchOperSpecData")

	collName := "subscriptionData.operatorSpecificData"
	ueId := request.Params["ueId"]
	patchItem := request.Body.([]models.PatchItem)

	problemDetails := PatchOperSpecDataProcedure(collName, ueId, patchItem)

	if problemDetails == nil {
		stats.IncrementUdrPolicyDataStats("update", "operator-specific-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrPolicyDataStats("update", "operator-specific-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func PatchOperSpecDataProcedure(collName string, ueId string, patchItem []models.PatchItem) *models.ProblemDetails {
	filter := bson.M{"ueId": ueId}

	origValue, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Errorln(errGetOne)
	}

	patchJSON, err := json.Marshal(patchItem)
	if err != nil {
		logger.DataRepoLog.Errorln(err)
	}

	failure := CommonDBClient.RestfulAPIJSONPatch(collName, filter, patchJSON)

	if failure == nil {
		newValue, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Errorln(errGetOne)
		}
		PreHandleOnDataChangeNotify(ueId, CurrentResourceUri, patchItem, origValue, newValue)
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandleQueryOperSpecData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryOperSpecData")

	ueId := request.Params["ueId"]
	collName := "subscriptionData.operatorSpecificData"

	response, problemDetails := QueryOperSpecDataProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrPolicyDataStats("get", "operator-specific-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrPolicyDataStats("get", "operator-specific-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrPolicyDataStats("get", "operator-specific-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryOperSpecDataProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	operatorSpecificDataContainer, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	// The key of the map is operator specific data element name and the value is the operator specific data of the UE.

	if operatorSpecificDataContainer != nil {
		return &operatorSpecificDataContainer, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleGetppData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle GetppData")

	collName := "subscriptionData.ppData"
	ueId := request.Params["ueId"]

	response, problemDetails := GetppDataProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "pp-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "pp-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "pp-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func GetppDataProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	ppData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if ppData != nil {
		return &ppData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleCreateSessionManagementData(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandleDeleteSessionManagementData(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandleQuerySessionManagementData(request *httpwrapper.Request) *httpwrapper.Response {
	return httpwrapper.NewResponse(http.StatusOK, nil, map[string]interface{}{})
}

func HandleQueryProvisionedData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryProvisionedData")

	var provisionedDataSets models.ProvisionedDataSets
	ueId := request.Params["ueId"]
	servingPlmnId := request.Params["servingPlmnId"]

	response, problemDetails := QueryProvisionedDataProcedure(ueId, servingPlmnId, provisionedDataSets)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "provisioned-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "provisioned-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "provisioned-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryProvisionedDataProcedure(ueId string, servingPlmnId string,
	provisionedDataSets models.ProvisionedDataSets,
) (*models.ProvisionedDataSets, *models.ProblemDetails) {
	{
		collName := "subscriptionData.provisionedData.amData"
		filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
		accessAndMobilitySubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Warnln(errGetOne)
		}
		if accessAndMobilitySubscriptionData != nil {
			var tmp models.AccessAndMobilitySubscriptionData
			err := mapstructure.Decode(accessAndMobilitySubscriptionData, &tmp)
			if err != nil {
				panic(err)
			}
			provisionedDataSets.AmData = &tmp
		}
	}

	{
		collName := "subscriptionData.provisionedData.smfSelectionSubscriptionData"
		filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
		smfSelectionSubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Warnln(errGetOne)
		}
		if smfSelectionSubscriptionData != nil {
			var tmp models.SmfSelectionSubscriptionData
			err := mapstructure.Decode(smfSelectionSubscriptionData, &tmp)
			if err != nil {
				panic(err)
			}
			provisionedDataSets.SmfSelData = &tmp
		}
	}

	{
		collName := "subscriptionData.provisionedData.smsData"
		filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
		smsSubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Warnln(errGetOne)
		}
		if smsSubscriptionData != nil {
			var tmp models.SmsSubscriptionData
			err := mapstructure.Decode(smsSubscriptionData, &tmp)
			if err != nil {
				panic(err)
			}
			provisionedDataSets.SmsSubsData = &tmp
		}
	}

	{
		collName := "subscriptionData.provisionedData.smData"
		filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
		sessionManagementSubscriptionDatas, errGetMany := CommonDBClient.RestfulAPIGetMany(collName, filter)
		if errGetMany != nil {
			logger.DataRepoLog.Warnln(errGetMany)
		}
		if sessionManagementSubscriptionDatas != nil {
			var tmp []models.SessionManagementSubscriptionData
			err := mapstructure.Decode(sessionManagementSubscriptionDatas, &tmp)
			if err != nil {
				panic(err)
			}
			provisionedDataSets.SmData = tmp
		}
	}

	{
		collName := "subscriptionData.provisionedData.traceData"
		filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
		traceData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Warnln(errGetOne)
		}
		if traceData != nil {
			var tmp models.TraceData
			err := mapstructure.Decode(traceData, &tmp)
			if err != nil {
				panic(err)
			}
			provisionedDataSets.TraceData = &tmp
		}
	}

	{
		collName := "subscriptionData.provisionedData.smsMngData"
		filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
		smsManagementSubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Warnln(errGetOne)
		}
		if smsManagementSubscriptionData != nil {
			var tmp models.SmsManagementSubscriptionData
			err := mapstructure.Decode(smsManagementSubscriptionData, &tmp)
			if err != nil {
				panic(err)
			}
			provisionedDataSets.SmsMngData = &tmp
		}
	}

	if !reflect.DeepEqual(provisionedDataSets, models.ProvisionedDataSets{}) {
		return &provisionedDataSets, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleModifyPpData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle ModifyPpData")

	collName := "subscriptionData.ppData"
	patchItem := request.Body.([]models.PatchItem)
	ueId := request.Params["ueId"]

	problemDetails := ModifyPpDataProcedure(collName, ueId, patchItem)
	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "pp-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "pp-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func ModifyPpDataProcedure(collName string, ueId string, patchItem []models.PatchItem) *models.ProblemDetails {
	filter := bson.M{"ueId": ueId}

	origValue, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	patchJSON, err := json.Marshal(patchItem)
	if err != nil {
		logger.DataRepoLog.Errorln(err)
	}

	failure := CommonDBClient.RestfulAPIJSONPatch(collName, filter, patchJSON)

	if failure == nil {
		newValue, errGetOneNew := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOneNew != nil {
			logger.DataRepoLog.Warnln(errGetOneNew)
		}
		PreHandleOnDataChangeNotify(ueId, CurrentResourceUri, patchItem, origValue, newValue)
		return nil
	} else {
		return util.ProblemDetailsModifyNotAllowed("")
	}
}

func HandleGetIdentityData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle GetIdentityData")

	ueId := request.Params["ueId"]
	collName := "subscriptionData.identityData"

	response, problemDetails := GetIdentityDataProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "identity-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "identity-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "identity-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func GetIdentityDataProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	identityData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if identityData != nil {
		return &identityData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleGetOdbData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle GetOdbData")

	ueId := request.Params["ueId"]
	collName := "subscriptionData.operatorDeterminedBarringData"

	response, problemDetails := GetOdbDataProcedure(collName, ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "operator-determined-barring-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "operator-determined-barring-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "operator-determined-barring-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func GetOdbDataProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	operatorDeterminedBarringData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if operatorDeterminedBarringData != nil {
		return &operatorDeterminedBarringData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleGetSharedData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle GetSharedData")

	var sharedDataIds []string
	if len(request.Query["shared-data-ids"]) != 0 {
		sharedDataIds = request.Query["shared-data-ids"]
		if strings.Contains(sharedDataIds[0], ",") {
			sharedDataIds = strings.Split(sharedDataIds[0], ",")
		}
	}
	collName := "subscriptionData.sharedData"

	response, problemDetails := GetSharedDataProcedure(collName, sharedDataIds)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "shared-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "shared-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "shared-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func GetSharedDataProcedure(collName string, sharedDataIds []string) (*[]map[string]interface{},
	*models.ProblemDetails,
) {
	var sharedDataArray []map[string]interface{}
	for _, sharedDataId := range sharedDataIds {
		filter := bson.M{"sharedDataId": sharedDataId}
		sharedData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
		if errGetOne != nil {
			logger.DataRepoLog.Warnln(errGetOne)
		}
		if sharedData != nil {
			sharedDataArray = append(sharedDataArray, sharedData)
		}
	}

	if sharedDataArray != nil {
		return &sharedDataArray, nil
	} else {
		return nil, util.ProblemDetailsNotFound("DATA_NOT_FOUND")
	}
}

func HandleRemovesdmSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle RemovesdmSubscriptions")

	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]

	problemDetails := RemovesdmSubscriptionsProcedure(ueId, subsId)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("delete", "sdm-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("delete", "sdm-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func RemovesdmSubscriptionsProcedure(ueId string, subsId string) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	_, ok = UESubsData.SdmSubscriptions[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	delete(UESubsData.SdmSubscriptions, subsId)

	return nil
}

func HandleUpdatesdmsubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle Updatesdmsubscriptions")

	ueId := request.Params["ueId"]
	subsId := request.Params["subsId"]
	SdmSubscription := request.Body.(models.SdmSubscription)

	problemDetails := UpdatesdmsubscriptionsProcedure(ueId, subsId, SdmSubscription)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("update", "sdm-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("update", "sdm-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func UpdatesdmsubscriptionsProcedure(ueId string, subsId string,
	SdmSubscription models.SdmSubscription,
) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	_, ok = UESubsData.SdmSubscriptions[subsId]

	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	SdmSubscription.SubscriptionId = subsId
	UESubsData.SdmSubscriptions[subsId] = &SdmSubscription

	return nil
}

func HandleCreateSdmSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateSdmSubscriptions")

	SdmSubscription := request.Body.(models.SdmSubscription)
	collName := SUBSCDATA_CTXDATA_AMF_NON3GPPACCESS
	ueId := request.Params["ueId"]

	locationHeader, SdmSubscription := CreateSdmSubscriptionsProcedure(SdmSubscription, collName, ueId)

	headers := http.Header{}
	headers.Set("Location", locationHeader)
	stats.IncrementUdrSubscriptionDataStats("create", "sdm-subscriptions", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusCreated, headers, SdmSubscription)
}

func CreateSdmSubscriptionsProcedure(SdmSubscription models.SdmSubscription,
	collName string, ueId string,
) (string, models.SdmSubscription) {
	udrSelf := udr_context.UDR_Self()

	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		udrSelf.UESubsCollection.Store(ueId, new(udr_context.UESubsData))
		value, _ = udrSelf.UESubsCollection.Load(ueId)
	}
	UESubsData := value.(*udr_context.UESubsData)
	if UESubsData.SdmSubscriptions == nil {
		UESubsData.SdmSubscriptions = make(map[string]*models.SdmSubscription)
	}

	newSubscriptionID := strconv.Itoa(udrSelf.SdmSubscriptionIDGenerator)
	SdmSubscription.SubscriptionId = newSubscriptionID
	UESubsData.SdmSubscriptions[newSubscriptionID] = &SdmSubscription
	udrSelf.SdmSubscriptionIDGenerator++

	/* Contains the URI of the newly created resource, according
	   to the structure: {apiRoot}/subscription-data/{ueId}/context-data/sdm-subscriptions/{subsId}' */
	locationHeader := fmt.Sprintf("%s/subscription-data/%s/context-data/sdm-subscriptions/%s",
		udrSelf.GetIPv4GroupUri(udr_context.NUDR_DR), ueId, newSubscriptionID)

	return locationHeader, SdmSubscription
}

func HandleQuerysdmsubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle Querysdmsubscriptions")

	ueId := request.Params["ueId"]

	response, problemDetails := QuerysdmsubscriptionsProcedure(ueId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sdm-subscriptions", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sdm-subscriptions", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "sdm-subscriptions", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QuerysdmsubscriptionsProcedure(ueId string) (*[]models.SdmSubscription, *models.ProblemDetails) {
	udrSelf := udr_context.UDR_Self()

	value, ok := udrSelf.UESubsCollection.Load(ueId)
	if !ok {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}

	UESubsData := value.(*udr_context.UESubsData)
	var sdmSubscriptionSlice []models.SdmSubscription

	for _, v := range UESubsData.SdmSubscriptions {
		sdmSubscriptionSlice = append(sdmSubscriptionSlice, *v)
	}
	return &sdmSubscriptionSlice, nil
}

func HandleQuerySmData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmData")

	collName := "subscriptionData.provisionedData.smData"
	ueId := request.Params["ueId"]
	servingPlmnId := request.Params["servingPlmnId"]
	singleNssai := models.Snssai{}
	singleNssaiQuery := request.Query.Get("single-nssai")
	err := json.Unmarshal([]byte(singleNssaiQuery), &singleNssai)
	if err != nil {
		logger.DataRepoLog.Warnln(err)
	}

	dnn := request.Query.Get("dnn")
	response := QuerySmDataProcedure(collName, ueId, servingPlmnId, singleNssai, dnn)
	stats.IncrementUdrSubscriptionDataStats("get", "sm-data", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusOK, nil, response)
}

func QuerySmDataProcedure(collName string, ueId string, servingPlmnId string,
	singleNssai models.Snssai, dnn string,
) *[]map[string]interface{} {
	filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}

	if !reflect.DeepEqual(singleNssai, models.Snssai{}) {
		if singleNssai.Sd == "" {
			filter["singleNssai.sst"] = singleNssai.Sst
		} else {
			filter["singleNssai.sst"] = singleNssai.Sst
			filter["singleNssai.sd"] = singleNssai.Sd
		}
	}

	if dnn != "" {
		filter["dnnConfigurations."+dnn] = bson.M{"$exists": true}
	}

	sessionManagementSubscriptionDatas, errGetMany := CommonDBClient.RestfulAPIGetMany(collName, filter)
	if errGetMany != nil {
		logger.DataRepoLog.Warnln(errGetMany)
	}

	return &sessionManagementSubscriptionDatas
}

func HandleCreateSmfContextNon3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateSmfContextNon3gpp")

	SmfRegistration := request.Body.(models.SmfRegistration)
	collName := SUBSCDATA_CTXDATA_SMF_REGISTRATION
	ueId := request.Params["ueId"]
	pduSessionId, err := strconv.ParseInt(request.Params["pduSessionId"], 10, 64)
	if err != nil {
		logger.DataRepoLog.Warnln(err)
	}

	response, status := CreateSmfContextNon3gppProcedure(SmfRegistration, collName, ueId, pduSessionId)

	switch status {
	case http.StatusCreated:
		stats.IncrementUdrSubscriptionDataStats("create", "smf-registrations", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusCreated, nil, response)
	case http.StatusOK:
		stats.IncrementUdrSubscriptionDataStats("create", "smf-registrations", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("create", "smf-registrations", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func CreateSmfContextNon3gppProcedure(SmfRegistration models.SmfRegistration,
	collName string, ueId string, pduSessionIdInt int64,
) (bson.M, int) {
	putData := util.ToBsonM(SmfRegistration)
	putData["ueId"] = ueId
	putData["pduSessionId"] = int32(pduSessionIdInt)

	filter := bson.M{"ueId": ueId, "pduSessionId": pduSessionIdInt}
	isExisted, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}

	if !isExisted {
		return putData, http.StatusCreated
	} else {
		return putData, http.StatusOK
	}
}

func HandleDeleteSmfContext(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle DeleteSmfContext")

	collName := SUBSCDATA_CTXDATA_SMF_REGISTRATION
	ueId := request.Params["ueId"]
	pduSessionId := request.Params["pduSessionId"]

	DeleteSmfContextProcedure(collName, ueId, pduSessionId)
	stats.IncrementUdrSubscriptionDataStats("delete", "smf-registrations", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func DeleteSmfContextProcedure(collName string, ueId string, pduSessionId string) {
	pduSessionIdInt, err := strconv.ParseInt(pduSessionId, 10, 32)
	if err != nil {
		logger.DataRepoLog.Error(err)
	}
	filter := bson.M{"ueId": ueId, "pduSessionId": pduSessionIdInt}

	errDelOne := CommonDBClient.RestfulAPIDeleteOne(collName, filter)
	if errDelOne != nil {
		logger.DataRepoLog.Warnln(errDelOne)
	}
}

func HandleQuerySmfRegistration(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmfRegistration")

	ueId := request.Params["ueId"]
	pduSessionId := request.Params["pduSessionId"]
	collName := SUBSCDATA_CTXDATA_SMF_REGISTRATION

	response, problemDetails := QuerySmfRegistrationProcedure(collName, ueId, pduSessionId)
	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "smf-registrations", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "smf-registrations", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "smf-registrations", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QuerySmfRegistrationProcedure(collName string, ueId string,
	pduSessionId string,
) (*map[string]interface{}, *models.ProblemDetails) {
	pduSessionIdInt, err := strconv.ParseInt(pduSessionId, 10, 32)
	if err != nil {
		logger.DataRepoLog.Error(err)
	}

	filter := bson.M{"ueId": ueId, "pduSessionId": pduSessionIdInt}

	smfRegistration, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if smfRegistration != nil {
		return &smfRegistration, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleQuerySmfRegList(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmfRegList")

	collName := SUBSCDATA_CTXDATA_SMF_REGISTRATION
	ueId := request.Params["ueId"]
	response := QuerySmfRegListProcedure(collName, ueId)

	stats.IncrementUdrSubscriptionDataStats("get", "smf-registrations", "SUCCESS")
	if response == nil {
		return httpwrapper.NewResponse(http.StatusOK, nil, []map[string]interface{}{})
	} else {
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	}
}

func QuerySmfRegListProcedure(collName string, ueId string) *[]map[string]interface{} {
	filter := bson.M{"ueId": ueId}
	smfRegList, errGetMany := CommonDBClient.RestfulAPIGetMany(collName, filter)
	if errGetMany != nil {
		logger.DataRepoLog.Warnln(errGetMany)
	}

	if smfRegList != nil {
		return &smfRegList
	} else {
		// Return empty array instead
		return nil
	}
}

func HandleQuerySmfSelectData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmfSelectData")

	collName := "subscriptionData.provisionedData.smfSelectionSubscriptionData"
	ueId := request.Params["ueId"]
	servingPlmnId := request.Params["servingPlmnId"]
	response, problemDetails := QuerySmfSelectDataProcedure(collName, ueId, servingPlmnId)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("get", "provisioned-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else {
		stats.IncrementUdrSubscriptionDataStats("get", "provisioned-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func QuerySmfSelectDataProcedure(collName string, ueId string,
	servingPlmnId string,
) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
	smfSelectionSubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if smfSelectionSubscriptionData != nil {
		return &smfSelectionSubscriptionData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleCreateSmsfContext3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateSmsfContext3gpp")

	SmsfRegistration := request.Body.(models.SmsfRegistration)
	collName := SUBSCDATA_CTXDATA_SMSF_3GPPACCESS
	ueId := request.Params["ueId"]

	CreateSmsfContext3gppProcedure(collName, ueId, SmsfRegistration)
	stats.IncrementUdrSubscriptionDataStats("create", "smsf-3gpp-access", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func CreateSmsfContext3gppProcedure(collName string, ueId string, SmsfRegistration models.SmsfRegistration) {
	putData := util.ToBsonM(SmsfRegistration)
	putData["ueId"] = ueId
	filter := bson.M{"ueId": ueId}

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
}

func HandleDeleteSmsfContext3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle DeleteSmsfContext3gpp")

	collName := SUBSCDATA_CTXDATA_SMSF_3GPPACCESS
	ueId := request.Params["ueId"]

	DeleteSmsfContext3gppProcedure(collName, ueId)
	stats.IncrementUdrSubscriptionDataStats("delete", "smsf-3gpp-access", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func DeleteSmsfContext3gppProcedure(collName string, ueId string) {
	filter := bson.M{"ueId": ueId}
	errDelOne := CommonDBClient.RestfulAPIDeleteOne(collName, filter)
	if errDelOne != nil {
		logger.DataRepoLog.Warnln(errDelOne)
	}
}

func HandleQuerySmsfContext3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmsfContext3gpp")

	collName := SUBSCDATA_CTXDATA_SMSF_3GPPACCESS
	ueId := request.Params["ueId"]

	response, problemDetails := QuerySmsfContext3gppProcedure(collName, ueId)
	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "smsf-3gpp-access", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "smsf-3gpp-access", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "smsf-3gpp-access", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QuerySmsfContext3gppProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	smsfRegistration, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if smsfRegistration != nil {
		return &smsfRegistration, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleCreateSmsfContextNon3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle CreateSmsfContextNon3gpp")

	SmsfRegistration := request.Body.(models.SmsfRegistration)
	collName := SUBSCDATA_CTXDATA_SMSF_NON3GPPACCESS
	ueId := request.Params["ueId"]

	CreateSmsfContextNon3gppProcedure(SmsfRegistration, collName, ueId)
	stats.IncrementUdrSubscriptionDataStats("create", "smsf-non-3gpp-access", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func CreateSmsfContextNon3gppProcedure(SmsfRegistration models.SmsfRegistration, collName string, ueId string) {
	putData := util.ToBsonM(SmsfRegistration)
	putData["ueId"] = ueId
	filter := bson.M{"ueId": ueId}

	_, errPutOne := CommonDBClient.RestfulAPIPutOne(collName, filter, putData)
	if errPutOne != nil {
		logger.DataRepoLog.Warnln(errPutOne)
	}
}

func HandleDeleteSmsfContextNon3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle DeleteSmsfContextNon3gpp")

	collName := SUBSCDATA_CTXDATA_SMSF_NON3GPPACCESS
	ueId := request.Params["ueId"]

	DeleteSmsfContextNon3gppProcedure(collName, ueId)
	stats.IncrementUdrSubscriptionDataStats("delete", "smsf-non-3gpp-access", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
}

func DeleteSmsfContextNon3gppProcedure(collName string, ueId string) {
	filter := bson.M{"ueId": ueId}
	errDelOne := CommonDBClient.RestfulAPIDeleteOne(collName, filter)
	if errDelOne != nil {
		logger.DataRepoLog.Warnln(errDelOne)
	}
}

func HandleQuerySmsfContextNon3gpp(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmsfContextNon3gpp")

	ueId := request.Params["ueId"]
	collName := SUBSCDATA_CTXDATA_SMSF_NON3GPPACCESS

	response, problemDetails := QuerySmsfContextNon3gppProcedure(collName, ueId)
	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "smsf-non-3gpp-access", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "smsf-non-3gpp-access", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "smsf-non-3gpp-access", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QuerySmsfContextNon3gppProcedure(collName string, ueId string) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId}

	smsfRegistration, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if smsfRegistration != nil {
		return &smsfRegistration, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleQuerySmsMngData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmsMngData")

	collName := "subscriptionData.provisionedData.smsMngData"
	ueId := request.Params["ueId"]
	servingPlmnId := request.Params["servingPlmnId"]
	response, problemDetails := QuerySmsMngDataProcedure(collName, ueId, servingPlmnId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sms-mng-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sms-mng-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "sms-mng-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QuerySmsMngDataProcedure(collName string, ueId string,
	servingPlmnId string,
) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}
	smsManagementSubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if smsManagementSubscriptionData != nil {
		return &smsManagementSubscriptionData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandleQuerySmsData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QuerySmsData")

	ueId := request.Params["ueId"]
	servingPlmnId := request.Params["servingPlmnId"]
	collName := "subscriptionData.provisionedData.smsData"

	response, problemDetails := QuerySmsDataProcedure(collName, ueId, servingPlmnId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sms-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "sms-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "sms-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QuerySmsDataProcedure(collName string, ueId string,
	servingPlmnId string,
) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}

	smsSubscriptionData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if smsSubscriptionData != nil {
		return &smsSubscriptionData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}

func HandlePostSubscriptionDataSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle PostSubscriptionDataSubscriptions")

	SubscriptionDataSubscriptions := request.Body.(models.SubscriptionDataSubscriptions)

	locationHeader := PostSubscriptionDataSubscriptionsProcedure(SubscriptionDataSubscriptions)

	headers := http.Header{}
	headers.Set("Location", locationHeader)
	stats.IncrementUdrSubscriptionDataStats("create", "subs-to-notify", "SUCCESS")
	return httpwrapper.NewResponse(http.StatusCreated, headers, SubscriptionDataSubscriptions)
}

func PostSubscriptionDataSubscriptionsProcedure(
	SubscriptionDataSubscriptions models.SubscriptionDataSubscriptions,
) string {
	udrSelf := udr_context.UDR_Self()

	newSubscriptionID := strconv.Itoa(udrSelf.SubscriptionDataSubscriptionIDGenerator)
	udrSelf.SubscriptionDataSubscriptions[newSubscriptionID] = &SubscriptionDataSubscriptions
	udrSelf.SubscriptionDataSubscriptionIDGenerator++

	/* Contains the URI of the newly created resource, according
	   to the structure: {apiRoot}/subscription-data/subs-to-notify/{subsId} */
	locationHeader := fmt.Sprintf("%s/subscription-data/subs-to-notify/%s",
		udrSelf.GetIPv4GroupUri(udr_context.NUDR_DR), newSubscriptionID)

	return locationHeader
}

func HandleRemovesubscriptionDataSubscriptions(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle RemovesubscriptionDataSubscriptions")

	subsId := request.Params["subsId"]

	problemDetails := RemovesubscriptionDataSubscriptionsProcedure(subsId)

	if problemDetails == nil {
		stats.IncrementUdrSubscriptionDataStats("delete", "subs-to-notify", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusNoContent, nil, map[string]interface{}{})
	} else {
		stats.IncrementUdrSubscriptionDataStats("delete", "subs-to-notify", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}
}

func RemovesubscriptionDataSubscriptionsProcedure(subsId string) *models.ProblemDetails {
	udrSelf := udr_context.UDR_Self()
	_, ok := udrSelf.SubscriptionDataSubscriptions[subsId]
	if !ok {
		return util.ProblemDetailsNotFound("SUBSCRIPTION_NOT_FOUND")
	}
	delete(udrSelf.SubscriptionDataSubscriptions, subsId)
	return nil
}

func HandleQueryTraceData(request *httpwrapper.Request) *httpwrapper.Response {
	logger.DataRepoLog.Infoln("handle QueryTraceData")

	collName := "subscriptionData.provisionedData.traceData"
	ueId := request.Params["ueId"]
	servingPlmnId := request.Params["servingPlmnId"]

	response, problemDetails := QueryTraceDataProcedure(collName, ueId, servingPlmnId)

	if response != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "trace-data", "SUCCESS")
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	} else if problemDetails != nil {
		stats.IncrementUdrSubscriptionDataStats("get", "trace-data", "FAILURE")
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	pd := util.ProblemDetailsUnspecified("")
	stats.IncrementUdrSubscriptionDataStats("get", "trace-data", "FAILURE")
	return httpwrapper.NewResponse(int(pd.Status), nil, pd)
}

func QueryTraceDataProcedure(collName string, ueId string,
	servingPlmnId string,
) (*map[string]interface{}, *models.ProblemDetails) {
	filter := bson.M{"ueId": ueId, "servingPlmnId": servingPlmnId}

	traceData, errGetOne := CommonDBClient.RestfulAPIGetOne(collName, filter)
	if errGetOne != nil {
		logger.DataRepoLog.Warnln(errGetOne)
	}

	if traceData != nil {
		return &traceData, nil
	} else {
		return nil, util.ProblemDetailsNotFound("USER_NOT_FOUND")
	}
}
