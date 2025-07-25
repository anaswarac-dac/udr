// SPDX-FileCopyrightText: 2021 Open Networking Foundation <info@opennetworking.org>
// Copyright 2019 free5GC.org
//
// SPDX-License-Identifier: Apache-2.0
//

/*
 * UDR Configuration Factory
 */

package factory

import (
	protos "github.com/5GC-DEV/config5g-cdac/proto/sdcoreConfig"
	"github.com/omec-project/openapi/models"
	"github.com/omec-project/udr/logger"
	utilLogger "github.com/omec-project/util/logger"
)

const (
	UDR_EXPECTED_CONFIG_VERSION = "1.0.0"
)

type Config struct {
	Info          *Info              `yaml:"info"`
	Configuration *Configuration     `yaml:"configuration"`
	Logger        *utilLogger.Logger `yaml:"logger"`
	CfgLocation   string
}

type Info struct {
	Version     string `yaml:"version,omitempty"`
	Description string `yaml:"description,omitempty"`
}

const (
	UDR_DEFAULT_IPV4     = "127.0.0.4"
	UDR_DEFAULT_PORT     = "8000"
	UDR_DEFAULT_PORT_INT = 8000
)

type Configuration struct {
	Sbi             *Sbi              `yaml:"sbi"`
	Mongodb         *Mongodb          `yaml:"mongodb"`
	NrfUri          string            `yaml:"nrfUri"`
	WebuiUri        string            `yaml:"webuiUri"`
	PlmnSupportList []PlmnSupportItem `yaml:"plmnSupportList,omitempty"`
}

type PlmnSupportItem struct {
	PlmnId     models.PlmnId   `yaml:"plmnId"`
	SNssaiList []models.Snssai `yaml:"snssaiList,omitempty"`
}

type Sbi struct {
	Tls          *Tls   `yaml:"tls,omitempty"`
	Scheme       string `yaml:"scheme"`
	RegisterIPv4 string `yaml:"registerIPv4,omitempty"` // IP that is registered at NRF.
	BindingIPv4  string `yaml:"bindingIPv4,omitempty"`  // IP used to run the server in the node.
	Port         int    `yaml:"port"`
}

type Tls struct {
	Log string `yaml:"log"`
	Pem string `yaml:"pem"`
	Key string `yaml:"key"`
}

type Mongodb struct {
	Name           string `yaml:"name,omitempty"`
	Url            string `yaml:"url,omitempty"`
	AuthKeysDbName string `yaml:"authKeysDbName"`
	AuthUrl        string `yaml:"authUrl"`
}

var (
	ConfigPodTrigger      chan bool
	ConfigUpdateDbTrigger chan *UpdateDb
)

func init() {
	ConfigPodTrigger = make(chan bool)
	ConfigUpdateDbTrigger = make(chan *UpdateDb, 10)
}

func (c *Config) GetVersion() string {
	if c.Info != nil && c.Info.Version != "" {
		return c.Info.Version
	}
	return ""
}

func (c *Config) addSmPolicyInfo(nwSlice *protos.NetworkSlice, dbUpdateChannel chan *UpdateDb) error {
	for _, devGrp := range nwSlice.DeviceGroup {
		for _, imsi := range devGrp.Imsi {
			// Iterate over the IpDomainDetails slice
			for _, ipDomain := range devGrp.IpDomainDetails {
				smPolicyEntry := &SmPolicyUpdateEntry{
					Imsi:   imsi,
					Dnn:    ipDomain.DnnName, // Access DnnName from the IpDomain struct
					Snssai: nwSlice.Nssai,
				}
				dbUpdate := &UpdateDb{
					SmPolicyTable: smPolicyEntry,
				}
				dbUpdateChannel <- dbUpdate
			}
		}
	}
	return nil
}

func (c *Config) UpdateConfig(commChannel chan *protos.NetworkSliceResponse, dbUpdateChannel chan *UpdateDb) bool {
	var minConfig bool
	for rsp := range commChannel {
		logger.GrpcLog.Infoln("received updateConfig in the udr app:", rsp)
		for _, ns := range rsp.NetworkSlice {
			logger.GrpcLog.Infoln("network slice name", ns.Name)
			if ns.Site != nil {
				logger.GrpcLog.Infoln("network slice has site name present")
				site := ns.Site
				logger.GrpcLog.Infoln("site name", site.SiteName)
				if site.Plmn != nil {
					logger.GrpcLog.Infoln("plmn mcc", site.Plmn.Mcc)
					plmn := PlmnSupportItem{}
					plmn.PlmnId.Mnc = site.Plmn.Mnc
					plmn.PlmnId.Mcc = site.Plmn.Mcc
					found := false
					for _, cplmn := range UdrConfig.Configuration.PlmnSupportList {
						if (cplmn.PlmnId.Mnc == plmn.PlmnId.Mnc) && (cplmn.PlmnId.Mcc == plmn.PlmnId.Mcc) {
							found = true
							break
						}
					}
					if !found {
						UdrConfig.Configuration.PlmnSupportList = append(UdrConfig.Configuration.PlmnSupportList, plmn)
					}
				} else {
					logger.GrpcLog.Infoln("plmn not present in the message")
				}
			}
			err := c.addSmPolicyInfo(ns, dbUpdateChannel)
			if err != nil {
				logger.GrpcLog.Errorf("error in adding sm policy info to db %v", err)
			}
		}
		if !minConfig {
			// first slice Created
			if len(UdrConfig.Configuration.PlmnSupportList) > 0 {
				minConfig = true
				ConfigPodTrigger <- true
				logger.GrpcLog.Infoln("send config trigger to main routine")
			}
		} else {
			// all slices deleted
			if len(UdrConfig.Configuration.PlmnSupportList) == 0 {
				minConfig = false
				ConfigPodTrigger <- false
				logger.GrpcLog.Infoln("send config trigger to main routine")
			} else {
				ConfigPodTrigger <- true
				logger.GrpcLog.Infoln("send config trigger to main routine")
			}
		}
	}
	return true
}
