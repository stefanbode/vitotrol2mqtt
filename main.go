package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/maxatome/go-vitotrol"
)

var pConf *Config
var pVitotrol *vitotrol.Session
var mqttClient mqtt.Client

var customAttrRegEx = regexp.MustCompile(
	`^([a-zA-Z0-9_]+)[-_]0x([a-fA-F0-9]{1,4})\z`)

func updateDeviceAttr(deviceName string, attrName string, value string) {

	attrId := vitotrol.AttributesNames2IDs[attrName]

	if vitotrol.AttributesRef[attrId].Access == vitotrol.ReadWrite {
		fmt.Println(fmt.Sprintf("Setting %s to %s", attrName, value))
		for _, vdev := range pVitotrol.Devices {
			if vdev.DeviceName == deviceName {
				ch, err := vdev.WriteDataWait(pVitotrol, attrId, value)
				if err != nil {
					fmt.Fprintf(os.Stderr, "WriteData error: %s\n", err)
					break
				}
				if err = <-ch; err != nil {
					fmt.Fprintf(os.Stderr, "WriteData failed: %s\n", err)
					break
				}
				// update MQTT with the new value
				token := mqttClient.Publish(pConf.MQTT.Topic+"/"+vdev.DeviceName+"/"+attrName, 0, true, value)
				token.Wait()
			}
		}
	} else {
		fmt.Println(fmt.Sprintf("Cannot set readonly attribute %s to %s", attrName, value))
	}
}

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	if pConf != nil {
		var topicRegEx = regexp.MustCompile(pConf.MQTT.Topic + `\/(.*?)\/(.*?)\/set`)
		m := topicRegEx.FindStringSubmatch(msg.Topic())

		if m != nil {
			updateDeviceAttr(m[1], m[2], string(msg.Payload()))
		}
	}
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("Connected")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	fmt.Printf("Connect lost: %v", err)
}

func VitotrolInit(vconf *ConfigVitotrol) *vitotrol.Session {
	var err error
	for {
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\nSleeping before retrying...\n", err)
			time.Sleep(time.Duration(vconf.RetryTimeout) * time.Second)
		}

		pVitotrol = &vitotrol.Session{}

		fmt.Println("Vitotrol login...")
		err = pVitotrol.Login(vconf.Login, vconf.Password)
		if err != nil {
			err = fmt.Errorf("Login failed: %s", err)
			continue
		}

		fmt.Println("Vitotrol GetDevices...")
		err = pVitotrol.GetDevices()
		if err != nil {
			err = fmt.Errorf("GetDevices failed: %s", err)
			continue
		}
		if len(pVitotrol.Devices) == 0 {
			err = fmt.Errorf("No device found")
			continue
		}
		fmt.Printf("%d device(s) found\n", len(pVitotrol.Devices))
		return pVitotrol
	}
}

func getAttrValue(vdev *vitotrol.Device, attrID vitotrol.AttrID) (value interface{}) {
	value, _ = vitotrol.AttributesRef[attrID].
		Type.Vitodata2NativeValue(vdev.Attributes[attrID].Value)

	// uint64 handled from influx 1.4
	if vuint64, ok := value.(uint64); ok {
		value = int(vuint64)
	}
	return
}

func refreshDevice(device *vitotrol.Device, attrs []vitotrol.AttrID) bool {
	ch, err := device.RefreshDataWait(pVitotrol, attrs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "RefreshData error: %s\n", err)
		return false
	}
	if err = <-ch; err != nil {
		fmt.Fprintf(os.Stderr, "RefreshData failed: %s\n", err)
		return false
	}

	err = device.GetData(pVitotrol, attrs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetData error: %s\n", err)
		return false
	}

	fields := map[string]interface{}{}

	for _, attrID := range attrs {
		fields[vitotrol.AttributesRef[attrID].Name] =
			getAttrValue(device, attrID)
	}

	// Write the batch
	values, _ := json.Marshal(fields)
	fmt.Sprintln("%", values)
	for key, element := range fields {
		token := mqttClient.Publish(pConf.MQTT.Topic+"/"+device.DeviceName+"/"+key, 0, true, fmt.Sprint(element))
		token.Wait()
	}

	return true
}

func refreshDevices() bool {
	for _, device := range pVitotrol.Devices {

		//fmt.Fprintf(os.Stderr, "Refreshing data for device: %s\n", device.DeviceName)

		if !device.IsConnected {
			return false
		}

		// Check if this device has a configuration
		deviceConfig := pConf.GetConfigDevice(device.DeviceName, device.LocationName)
		if deviceConfig == nil {
			return false
		}

		if !refreshDevice(&device, deviceConfig.attrs) {
			return false
		}

		time.Sleep(time.Duration(pConf.Vitotrol.Frequency) * time.Second)

	}
	return true
}

func resolveFields() {
	for _, configDevice := range pConf.Devices {
		attrs := make(map[vitotrol.AttrID]struct{}, len(configDevice.Fields))

		for _, fieldName := range configDevice.Fields {
			// Computed attribute
			if computedAttr, ok := computedAttrs[fieldName]; ok {
				for _, attrID := range computedAttr.Attrs {
					attrs[attrID] = struct{}{}
				}
				configDevice.computedFields = append(configDevice.computedFields, fieldName)
			} else {
				// Already known attribute
				attrID, ok := vitotrol.AttributesNames2IDs[fieldName]
				if !ok {
					// Custom attribute

					m := customAttrRegEx.FindStringSubmatch(fieldName)
					if m == nil {
						fmt.Fprintf(os.Stderr, "Unknown attribute `%s'\n", fieldName)
						os.Exit(1)
					}

					attrRef := vitotrol.AttrRef{
						Type:   vitotrol.TypeDouble,
						Access: vitotrol.ReadOnly,
						Name:   m[1],
					}
					tmpID, _ := strconv.ParseUint(m[2], 16, 16)
					attrID = vitotrol.AttrID(tmpID)

					vitotrol.AddAttributeRef(attrID, attrRef)
				}

				attrs[attrID] = struct{}{}
			}
		}

		if len(attrs) == 0 {
			fmt.Fprintf(os.Stderr, "No attributes for device %s/location %s\n",
				configDevice.Name, configDevice.Location)
			os.Exit(1)
		}

		configDevice.attrs = make([]vitotrol.AttrID, 0, len(attrs))
		for attrID := range attrs {
			configDevice.attrs = append(configDevice.attrs, attrID)
		}
	}
}

func initializeMQTTClient() {
	opts := mqtt.NewClientOptions()
	opts.AddBroker("tcp://192.168.3.250:1883")
	opts.SetClientID(pConf.MQTT.ClientID)
	opts.SetUsername(pConf.MQTT.Login)
	opts.SetPassword(pConf.MQTT.Password)
	opts.SetAutoReconnect(true)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler
	mqttClient = mqtt.NewClient(opts)

	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	//subscribe to the topic to catch the control commands
	topic := pConf.MQTT.Topic + "/#"
	token := mqttClient.Subscribe(topic, 1, messagePubHandler)
	token.Wait()
}

func mainLoop() {
	for {
		pVitotrol = VitotrolInit(&pConf.Vitotrol)

		for {
			if !refreshDevices() {
				time.Sleep(time.Duration(pConf.Vitotrol.RetryTimeout) * time.Second)
				break
			}
		}
	}
}

func main() {
	//read configuration
	configFile := flag.String("config", "", "config file")

	flag.Parse()

	if *configFile == "" {
		fmt.Fprintln(os.Stderr, "config file is missing")
		os.Exit(1)
	}
	conf, err := ReadConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot read config: %s\n", err)
		os.Exit(1)
	}
	pConf = conf

	// Resolve fields
	resolveFields()

	// Create a new MQTT Client
	initializeMQTTClient()

	mainLoop()
}
