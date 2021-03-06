package main

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/rs/xid"

	"github.com/BurntSushi/toml"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.bug.st/serial"
)

var panasonicQuery []byte = []byte{0x71, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
var PANASONICQUERYSIZE int = 110

//should be the same number
var NUMBER_OF_TOPICS int = 92
var AllTopics [92]TopicData
var MqttKeepalive time.Duration
var CommandsToSend map[xid.ID][]byte

var actData [92]string
var config Config
var sending bool
var Serial serial.Port
var err error
var goodreads float64
var totalreads float64
var readpercentage float64

type command_struct struct {
	value  [128]byte
	length int
}

type TopicData struct {
	TopicNumber   int
	TopicName     string
	TopicBit      int
	TopicFunction string
	TopicUnit     string
	TopicA2M      string
}

type Config struct {
	Readonly               bool
	Loghex                 bool
	Device                 string
	ReadInterval           int
	MqttServer             string
	MqttPort               string
	MqttLogin              string
	Aquarea2mqttCompatible bool
	Mqtt_topic_base        string
	Mqtt_set_base          string
	Aquarea2mqttPumpID     string
	MqttPass               string
	MqttClientID           string
	MqttKeepalive          int
	ForceRefreshTime       int
	EnableCommand          bool
	SleepAfterCommand      int
	Configured             bool
}

var cfgfile *string
var topicfile *string
var configfile string

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

func HttpServ() {
	tmpl := template.Must(template.ParseFiles("/etc/gh/forms.html"))
	//tmpl := template.Must(template.ParseFiles("forms.html"))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			err := tmpl.Execute(w, config)
			fmt.Println("Dorarlo zapytanie inne niz post", err)
			return
		}

		config = Config{
			Readonly:               pb(r.FormValue("Readonly")),
			Loghex:                 pb(r.FormValue("Loghex")),
			Device:                 r.FormValue("Device"),
			ReadInterval:           pi(r.FormValue("ReadInterval")),
			MqttServer:             r.FormValue("MqttServer"),
			MqttPort:               r.FormValue("MqttPort"),
			MqttLogin:              r.FormValue("MqttLogin"),
			Aquarea2mqttCompatible: pb(r.FormValue("Aquarea2mqttCompatible")),
			Mqtt_topic_base:        r.FormValue("Mqtt_topic_base"),
			Mqtt_set_base:          r.FormValue("Mqtt_set_base"),
			Aquarea2mqttPumpID:     r.FormValue("Aquarea2mqttPumpID"),
			MqttPass:               r.FormValue("MqttPass"),
			MqttClientID:           r.FormValue("MqttClientID"),
			MqttKeepalive:          pi(r.FormValue("MqttKeepalive")),
			ForceRefreshTime:       pi(r.FormValue("ForceRefreshTime")),
			EnableCommand:          pb(r.FormValue("EnableCommand")),
			SleepAfterCommand:      pi(r.FormValue("SleepAfterCommand")),
			Configured:             pb(r.FormValue("Configured")),
		}

		f, err := os.Create(configfile)
		if err != nil {
			// failed to create/open the file
			log.Fatal(err)
		}
		if err := toml.NewEncoder(f).Encode(config); err != nil {
			// failed to encode
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			// failed to close the file
			log.Fatal(err)

		}

		tmpl.Execute(w, config)

	})

	http.HandleFunc("/states", func(w http.ResponseWriter, r *http.Request) {
		b, err := json.Marshal(actData)
		if err != nil {
			// failed to create/open the file
			fmt.Println(err)

		}
		w.Header().Set("Content-Type", "application/json;") // normal header
		fmt.Fprintf(w, string(b))
		w.WriteHeader(http.StatusOK)
	})

	http.ListenAndServe(":8080", nil)
}
func pb(s string) bool {
	bro, _ := strconv.ParseBool(s)
	return bro
}
func pi(s string) int {
	bro, _ := strconv.Atoi(s)
	return bro
}

func ReadConfig() Config {

	_, err := os.Stat(configfile)
	if err != nil {
		log.Fatal("Config file is missing: ", configfile)
	}

	var config Config
	if _, err := toml.DecodeFile(configfile, &config); err != nil {
		log.Fatal(err)
	}
	return config
}

func UpdateConfig(configfile string) bool {
	fmt.Printf("try to update configfile: %s", configfile)
	out, err := exec.Command("/usr/bin/usb_mount.sh").Output()
	if err != nil {
		fmt.Println(err.Error())
	}
	fmt.Println(out)
	_, err = os.Stat("/mnt/usb/GoHeishaMonConfig.new")
	if err != nil {
		_, _ = exec.Command("/usr/bin/usb_umount.sh").Output()
		return false
	}
	if GetFileChecksum(configfile) != GetFileChecksum("/mnt/usb/GoHeishaMonConfig.new") {
		fmt.Printf("checksum of configfile and new configfile diffrent: %s ", configfile)

		_, _ = exec.Command("/bin/cp", "/mnt/usb/GoHeishaMonConfig.new", configfile).Output()
		if err != nil {
			fmt.Printf("can't update configfile %s", configfile)
			return false
		}
	}

	return true
}

func UpdatePassword() bool {
	_, err = os.Stat("/mnt/usb/GoHeishaMonPassword.new")
	if err != nil {
		return true
	} else {
		_, _ = exec.Command("chmod", "+x", "/root/pass.sh").Output()
		dat, _ := ioutil.ReadFile("/mnt/usb/GoHeishaMonPassword.new")
		fmt.Printf("updejtuje haslo na: %s", string(dat))
		o, err := exec.Command("/root/pass.sh", string(dat)).Output()
		if err != nil {
			fmt.Println(err)
			fmt.Println(o)

			return false
		}
		fmt.Println(o)

		_, _ = exec.Command("/bin/rm", "/mnt/usb/GoHeishaMonPassword.new").Output()
	}
	return true
}

func GetFileChecksum(f string) string {
	input := strings.NewReader(f)

	hash := md5.New()
	if _, err := io.Copy(hash, input); err != nil {
		log.Fatal(err)
	}
	sum := hash.Sum(nil)

	return fmt.Sprintf("%x\n", sum)

}

func UpdateConfigLoop(configfile string) {
	for {
		time.Sleep(time.Minute * 5)
		UpdateConfig(configfile)
	}
}

func main() {
	//	cfgfile = flag.String("c", "config", "a config file patch")
	//	topicfile = flag.String("t", "Topics.csv", "a topic file patch")
	flag.Parse()
	configfile = "/etc/gh/config"
	//configfile = "config"
	_, err := os.Stat(configfile)
	if err != nil {
		fmt.Printf("Config file is missing: %s ", configfile)
		UpdateConfig(configfile)
	}
	go UpdateConfigLoop(configfile)
	c1 := make(chan bool, 1)
	go ClearActData()
	CommandsToSend = make(map[xid.ID][]byte)
	var in int
	config = ReadConfig()
	go HttpServ()
	for {
		if config.Configured == true {
			if config.Readonly != true {
				for {
					log_message("Not sending this command. Heishamon in listen only mode! - this POC version don't support writing yet....")
					//os.Exit(0)
					break
					time.Sleep(5 * time.Second)
				}
			}
			ports, err := serial.GetPortsList()
			if err != nil {
				fmt.Printf("%s", err)

			}
			if len(ports) == 0 {
				fmt.Printf("No serial ports found!")

			}
			for _, port := range ports {
				fmt.Printf("Found port: %v\n", port)

			}
			mode := &serial.Mode{
				BaudRate: 9600,
				Parity:   serial.EvenParity,
				DataBits: 8,
				StopBits: serial.OneStopBit,
			}
			Serial, err = serial.Open(config.Device, mode)
			if err != nil {
				fmt.Println(err)
				time.Sleep(5 * time.Second)

			} else {
				PoolInterval := time.Second * time.Duration(config.ReadInterval)
				ParseTopicList()
				MqttKeepalive = time.Second * time.Duration(config.MqttKeepalive)
				MC, MT := MakeMQTTConn()

				for {
					if MC.IsConnected() != true {
						MC, MT = MakeMQTTConn()
					}
					if len(CommandsToSend) > 0 {
						fmt.Println("jest wiecej niz jedna komenda tj", len(CommandsToSend))
						in = 1
						for key, value := range CommandsToSend {
							if in == 1 {

								send_command(value, len(value))
								delete(CommandsToSend, key)
								in++
								time.Sleep(time.Second * time.Duration(config.SleepAfterCommand))

							} else {
								fmt.Println("numer komenty  ", in, " jest za duzy zrobie to w nastepnym cyklu")
								break
							}
							fmt.Println("koncze range po tablicy z komendami ")

						}

					} else {
						send_command(panasonicQuery, PANASONICQUERYSIZE)
					}
					go func() {
						tbool := readSerial(MC, MT)
						c1 <- tbool
					}()

					select {
					case res := <-c1:
						fmt.Println("read ma status", res)
					case <-time.After(5 * time.Second):
						fmt.Println("out of time for read :(")
					}

					time.Sleep(PoolInterval)

				}
			}
		} else {
			fmt.Println("Program was not configured")
			time.Sleep(5 * time.Second)
		}
	}

}

func ClearActData() {
	for {
		time.Sleep(time.Second * time.Duration(config.ForceRefreshTime))
		for k, _ := range actData {
			actData[k] = "nil" //funny i know ;)
		}

	}
}

func MakeMQTTConn() (mqtt.Client, mqtt.Token) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("%s://%s:%s", "tcp", config.MqttServer, config.MqttPort))
	opts.SetPassword(config.MqttPass)
	opts.SetUsername(config.MqttLogin)
	opts.SetClientID(config.MqttClientID)
	opts.SetWill(config.Mqtt_set_base+"/LWT", "Online", 2, false)
	opts.SetKeepAlive(MqttKeepalive)
	opts.SetOnConnectHandler(startsub)
	opts.SetConnectionLostHandler(connLostHandler)

	// connect to broker
	client := mqtt.NewClient(opts)
	//defer client.Disconnect(uint(2))

	token := client.Connect()
	if token.Wait() && token.Error() != nil {
		fmt.Printf("Fail to connect broker, %v", token.Error())
	}
	return client, token
}

func connLostHandler(c mqtt.Client, err error) {
	fmt.Printf("Connection lost, reason: %v\n", err)

	//Perform additional action...
}

func startsub(c mqtt.Client) {
	c.Subscribe("aquarea/+/+/set", 2, HandleMSGfromMQTT)
	c.Subscribe(config.Mqtt_set_base+"/SetHeatpump", 2, HandleSetHeatpump)
	c.Subscribe(config.Mqtt_set_base+"/SetQuietMode", 2, HandleSetQuietMode)
	c.Subscribe(config.Mqtt_set_base+"/SetZ1HeatRequestTemperature", 2, HandleSetZ1HeatRequestTemperature)
	c.Subscribe(config.Mqtt_set_base+"/SetZ1CoolRequestTemperature", 2, HandleSetZ1CoolRequestTemperature)
	c.Subscribe(config.Mqtt_set_base+"/SetZ2HeatRequestTemperature", 2, HandleSetZ2HeatRequestTemperature)
	c.Subscribe(config.Mqtt_set_base+"/SetZ2CoolRequestTemperature", 2, HandleSetZ2CoolRequestTemperature)
	c.Subscribe(config.Mqtt_set_base+"/SetOperationMode", 2, HandleSetOperationMode)
	c.Subscribe(config.Mqtt_set_base+"/SetForceDHW", 2, HandleSetForceDHW)
	c.Subscribe(config.Mqtt_set_base+"/SetForceDefrost", 2, HandleSetForceDefrost)
	c.Subscribe(config.Mqtt_set_base+"/SetForceSterilization", 2, HandleSetForceSterilization)
	c.Subscribe(config.Mqtt_set_base+"/SetHolidayMode", 2, HandleSetHolidayMode)
	c.Subscribe(config.Mqtt_set_base+"/SetPowerfulMode", 2, HandleSetPowerfulMode)
	c.Subscribe(config.Mqtt_set_base+"/SetDHWTemp", 2, HandleSetDHWTemp)
	c.Subscribe(config.Mqtt_set_base+"/SendRawValue", 2, HandleSendRawValue)
	if config.EnableCommand == true {
		c.Subscribe(config.Mqtt_set_base+"/OSCommand", 2, HandleOSCommand)
	}

	//Perform additional action...
}

func HandleMSGfromMQTT(mclient mqtt.Client, msg mqtt.Message) {

}

func remove(slice []string, s int) []string {
	return append(slice[:s], slice[s+1:]...)
}

func HandleOSCommand(mclient mqtt.Client, msg mqtt.Message) {
	var cmd *exec.Cmd
	var out2 string
	s := strings.Split(string(msg.Payload()), " ")
	if len(s) < 2 {
		cmd = exec.Command(s[0])
	} else {
		cmd = exec.Command(s[0], s[1:]...)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		// TODO: handle error more gracefully
		out2 = fmt.Sprintf("%s", err)
	}
	comout := fmt.Sprintf("%s - %s", out, out2)
	TOP := fmt.Sprintf("%s/OSCommand/out", config.Mqtt_set_base)
	fmt.Println("Publikuje do ", TOP, "warosc", comout)
	token := mclient.Publish(TOP, byte(0), false, comout)
	if token.Wait() && token.Error() != nil {
		fmt.Printf("Fail to publish, %v", token.Error())
	}

}

func HandleSendRawValue(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	cts := strings.TrimSpace(string(msg.Payload()))
	command, err = hex.DecodeString(cts)
	if err != nil {
		fmt.Println(err)
	}

	CommandsToSend[xid.New()] = command
}

func HandleSetOperationMode(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var set_mode byte
	a, _ := strconv.Atoi(string(msg.Payload()))

	switch a {
	case 0:
		set_mode = 82
	case 1:
		set_mode = 83
	case 2:
		set_mode = 89
	case 3:
		set_mode = 33
	case 4:
		set_mode = 98
	case 5:
		set_mode = 99
	case 6:
		set_mode = 104
	default:
		set_mode = 0
	}

	fmt.Printf("set heat pump mode to  %d", set_mode)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, set_mode, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetDHWTemp(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var heatpump_state byte

	a, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		a = int(f)
	}

	e := a + 128
	heatpump_state = byte(e)
	fmt.Printf("set DHW temperature to   %d", a)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, heatpump_state, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetPowerfulMode(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var heatpump_state byte

	a, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		a = int(f)
	}

	e := a + 73
	heatpump_state = byte(e)
	fmt.Printf("set powerful mode to  %d", a)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, heatpump_state, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetHolidayMode(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var heatpump_state byte
	e := 16
	a, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		a = int(f)
	}

	if a == 1 {
		e = 32
	}
	heatpump_state = byte(e)
	fmt.Printf("set holiday mode to  %d", heatpump_state)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, heatpump_state, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetForceSterilization(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var heatpump_state byte
	e := 0
	a, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		a = int(f)
	}

	if a == 1 {
		e = 4
	}
	heatpump_state = byte(e)
	fmt.Printf("set force sterilization  mode to %d", heatpump_state)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, heatpump_state, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetForceDefrost(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var heatpump_state byte
	e := 0
	a, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		a = int(f)
	}

	if a == 1 {
		e = 2
	}
	heatpump_state = byte(e)
	fmt.Printf("set force defrost mode to %d", heatpump_state)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, heatpump_state, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetForceDHW(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var heatpump_state byte
	e := 64
	a, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		a = int(f)
	}

	if a == 1 {
		e = 128
	}
	heatpump_state = byte(e)
	fmt.Printf("set force DHW mode to %d", heatpump_state)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, heatpump_state, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetZ1HeatRequestTemperature(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var request_temp byte
	e, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		e = int(f)
	}

	e = e + 128
	request_temp = byte(e)
	fmt.Printf("set z1 heat request temperature to %d", request_temp-128)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, request_temp, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetZ1CoolRequestTemperature(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var request_temp byte
	e, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		e = int(f)
	}
	e = e + 128
	request_temp = byte(e)
	fmt.Printf("set z1 cool request temperature to %d", request_temp-128)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, request_temp, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetZ2HeatRequestTemperature(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var request_temp byte
	e, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		e = int(f)
	}
	e = e + 128
	request_temp = byte(e)
	fmt.Printf("set z2 heat request temperature to %d", request_temp-128)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, request_temp, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetZ2CoolRequestTemperature(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var request_temp byte
	e, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		e = int(f)
	}
	e = e + 128
	request_temp = byte(e)
	fmt.Printf("set z2 cool request temperature to %d", request_temp-128)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, request_temp, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetQuietMode(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var quiet_mode byte

	e, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		e = int(f)
	}
	e = (e + 1) * 8

	quiet_mode = byte(e)
	fmt.Printf("set Quiet mode to %d", quiet_mode/8-1)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, 0x00, 0x00, 0x00, quiet_mode, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func HandleSetHeatpump(mclient mqtt.Client, msg mqtt.Message) {
	var command []byte
	var heatpump_state byte

	e := 1
	a, er := strconv.Atoi(string(msg.Payload()))
	if er != nil {
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		a = int(f)
	}
	if a == 1 {
		e = 2
	}

	heatpump_state = byte(e)
	fmt.Printf("set heatpump state to %d", heatpump_state)
	command = []byte{0xf1, 0x6c, 0x01, 0x10, heatpump_state, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if config.Loghex == true {
		logHex(command, len(command))
	}
	CommandsToSend[xid.New()] = command
}

func log_message(a string) {
	fmt.Println(a)
}

func logHex(command []byte, length int) {
	fmt.Printf("% X \n", command)

}

func calcChecksum(command []byte, length int) byte {
	var chk byte
	chk = 0
	for i := 0; i < length; i++ {
		chk += command[i]
	}
	chk = (chk ^ 0xFF) + 01
	return chk
}

func ParseTopicList() {

	//tf := *topicfile
	tf := "/etc/gh/Topics.csv"
	lines, err := ReadCsv(tf)
	if err != nil {
		panic(err)
	}

	// Loop through lines & turn into object
	for _, line := range lines {
		TB, _ := strconv.Atoi(line[2])
		TNUM, _ := strconv.Atoi(line[0])
		data := TopicData{
			TopicNumber:   TNUM,
			TopicName:     line[1],
			TopicBit:      TB,
			TopicFunction: line[3],
			TopicUnit:     line[4],
			TopicA2M:      line[5],
		}
		AllTopics[TNUM] = data
		//a	fmt.Println(data)
	}

}

func ReadCsv(filename string) ([][]string, error) {

	// Open CSV file
	f, err := os.Open(filename)
	if err != nil {
		return [][]string{}, err
	}
	defer f.Close()

	// Read File into a Variable
	lines, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return [][]string{}, err
	}

	return lines, nil
}

func send_command(command []byte, length int) bool {

	var chk byte
	chk = calcChecksum(command, length)
	var bytesSent int

	bytesSent, err := Serial.Write(command) //first send command
	_, err = Serial.Write([]byte{chk})      //then calculcated checksum byte afterwards
	if err != nil {
		fmt.Println(err)
	}
	log_msg := fmt.Sprintf("sent bytes: %d with checksum: %d ", bytesSent, int(chk))
	log_message(log_msg)

	if config.Loghex == true {
		logHex(command, length)
	}
	//readSerial()
	//allowreadtime = millis() + SERIALTIMEOUT //set allowreadtime when to timeout the answer of this command
	return true
}

// func pushCommandBuffer(command []byte , length int) {
// 	if (commandsInBuffer < MAXCOMMANDSINBUFFER) {
// 	  command_struct* newCommand = new command_struct;
// 	  newCommand->length = length;
// 	  for (int i = 0 ; i < length ; i++) {
// 		newCommand->value[i] = command[i];
// 	  }
// 	  newCommand->next = commandBuffer;
// 	  commandBuffer = newCommand;
// 	  commandsInBuffer++;
// 	}
// 	else {
// 	  log_message("Too much commands already in buffer. Ignoring this commands.");
// 	}
//   }

func readSerial(MC mqtt.Client, MT mqtt.Token) bool {

	data_length := 203

	totalreads++
	data := make([]byte, data_length)
	n, err := Serial.Read(data)
	if err != nil {
		log.Fatal(err)
	}
	if n == 0 {
		fmt.Println("\nEOF")

	}

	//panasonic read is always 203 on valid receive, if not yet there wait for next read
	log_message("Received 203 bytes data\n")
	if config.Loghex {
		logHex(data, data_length)
	}
	if !isValidReceiveHeader(data) {
		log_message("Received wrong header!\n")
		data_length = 0 //for next attempt;
		return false
	}
	if !isValidReceiveChecksum(data) {
		log_message("Checksum received false!")
		data_length = 0 //for next attempt
		return false
	}
	log_message("Checksum and header received ok!")
	data_length = 0 //for next attempt
	goodreads++
	readpercentage = ((goodreads / totalreads) * 100)
	log_msg := fmt.Sprintf("Total reads : %f and total good reads : %f (%.2f %%)", totalreads, goodreads, readpercentage)
	log_message(log_msg)
	decode_heatpump_data(data, MC, MT)
	return true

}

func isValidReceiveHeader(data []byte) bool {
	return ((data[0] == 0x71) && (data[1] == 0xC8) && (data[2] == 0x01) && (data[3] == 0x10))
}

func isValidReceiveChecksum(data []byte) bool {
	var chk byte
	chk = 0
	for i := 0; i < len(data); i++ {
		chk += data[i]
	}
	return (chk == 0) //all received bytes + checksum should result in 0
}

func CallTopicFunction(data byte, f func(data byte) string) string {
	return f(data)
}

func getBit7and8(input byte) string {
	return fmt.Sprintf("%d", (input&0b11)-1)
}

func getBit3and4and5(input byte) string {
	return fmt.Sprintf("%d", ((input>>3)&0b111)-1)
}

func getIntMinus1Times10(input byte) string {
	value := int(input) - 1
	return fmt.Sprintf("%d", value*10)

}

func getIntMinus1Times50(input byte) string {
	value := int(input) - 1
	return fmt.Sprintf("%d", value*50)

}

func unknown(input byte) string {
	return "-1"
}

func getIntMinus128(input byte) string {
	value := int(input) - 128
	return fmt.Sprintf("%d", value)
}

func getIntMinus1Div5(input byte) string {
	value := int(input) - 1
	var out float32
	out = float32(value) / 5
	return fmt.Sprintf("%.2f", out)

}

func getRight3bits(input byte) string {
	return fmt.Sprintf("%d", (input&0b111)-1)

}

func getBit1and2(input byte) string {
	return fmt.Sprintf("%d", (input>>6)-1)

}

func getOpMode(input byte) string {
	switch int(input) {
	case 82:
		return "0"
	case 83:
		return "1"
	case 89:
		return "2"
	case 97:
		return "3"
	case 98:
		return "4"
	case 99:
		return "5"
	case 105:
		return "6"
	default:
		return "-1"
	}
}

func getIntMinus1(input byte) string {
	value := int(input) - 1
	return fmt.Sprintf("%d", value)
}

func getEnergy(input byte) string {
	value := (int(input) - 1) * 200
	return fmt.Sprintf("%d", value)
}

func getBit3and4(input byte) string {
	return fmt.Sprintf("%d", ((input>>4)&0b11)-1)

}

func getBit5and6(input byte) string {
	return fmt.Sprintf("%d", ((input>>2)&0b11)-1)

}

func getPumpFlow(data []byte) string { // TOP1 //
	PumpFlow1 := int(data[170])
	PumpFlow2 := ((float64(data[169]) - 1) / 256)
	PumpFlow := float64(PumpFlow1) + PumpFlow2
	//return String(PumpFlow,2);
	return fmt.Sprintf("%.2f", PumpFlow)
}

func getErrorInfo(data []byte) string { // TOP44 //
	Error_type := int(data[113])
	Error_number := int(data[114]) - 17
	var Error_string string
	switch Error_type {
	case 177: //B1=F type error
		Error_string = fmt.Sprintf("F%02X", Error_number)

	case 161: //A1=H type error
		Error_string = fmt.Sprintf("H%02X", Error_number)

	default:
		Error_string = fmt.Sprintf("No error")

	}
	return Error_string
}

func decode_heatpump_data(data []byte, mclient mqtt.Client, token mqtt.Token) {

	var updatenow bool = false
	m := map[string]func(byte) string{
		"getBit7and8":         getBit7and8,
		"unknown":             unknown,
		"getRight3bits":       getRight3bits,
		"getIntMinus1Div5":    getIntMinus1Div5,
		"getIntMinus1Times50": getIntMinus1Times50,
		"getIntMinus1Times10": getIntMinus1Times10,
		"getBit3and4and5":     getBit3and4and5,
		"getIntMinus128":      getIntMinus128,
		"getBit1and2":         getBit1and2,
		"getOpMode":           getOpMode,
		"getIntMinus1":        getIntMinus1,
		"getEnergy":           getEnergy,
		"getBit5and6":         getBit5and6,

		"getBit3and4": getBit3and4,
	}

	// 	if (millis() > nextalldatatime) {
	// 	  updatenow = true;
	// 	  nextalldatatime = millis() + UPDATEALLTIME;
	// 	}
	for k, v := range AllTopics {
		var Input_Byte byte
		var Topic_Value string
		var value string
		switch k {
		case 1:
			Topic_Value = getPumpFlow(data)
		case 11:
			d := make([]byte, 2)
			d[0] = data[183]
			d[1] = data[182]
			Topic_Value = fmt.Sprintf("%d", int(binary.BigEndian.Uint16(d))-1)
		case 12:
			d := make([]byte, 2)
			d[0] = data[180]
			d[1] = data[179]
			Topic_Value = fmt.Sprintf("%d", int(binary.BigEndian.Uint16(d))-1)
		case 90:
			d := make([]byte, 2)
			d[0] = data[186]
			d[1] = data[185]
			Topic_Value = fmt.Sprintf("%d", int(binary.BigEndian.Uint16(d))-1)
		case 91:
			d := make([]byte, 2)
			d[0] = data[189]
			d[1] = data[188]
			Topic_Value = fmt.Sprintf("%d", int(binary.BigEndian.Uint16(d))-1)
		case 44:
			Topic_Value = getErrorInfo(data)
		default:
			Input_Byte = data[v.TopicBit]
			if _, ok := m[v.TopicFunction]; ok {
				Topic_Value = CallTopicFunction(Input_Byte, m[v.TopicFunction])
			} else {
				fmt.Println("NIE MA FUNKCJI", v.TopicFunction)
			}

		}

		if (updatenow) || (actData[k] != Topic_Value) {
			actData[k] = Topic_Value
			fmt.Printf("received TOP%d %s: %s \n", k, v.TopicName, Topic_Value)
			if config.Aquarea2mqttCompatible {
				TOP := "aquarea/state/" + fmt.Sprintf("%s/%s", config.Aquarea2mqttPumpID, v.TopicA2M)
				value = strings.TrimSpace(Topic_Value)
				value = strings.ToUpper(Topic_Value)
				fmt.Println("Publikuje do ", TOP, "warosc", value)
				token = mclient.Publish(TOP, byte(0), false, value)
				if token.Wait() && token.Error() != nil {
					fmt.Printf("Fail to publish, %v", token.Error())
				}
			}
			TOP := fmt.Sprintf("%s/%s", config.Mqtt_topic_base, v.TopicName)
			fmt.Println("Publikuje do ", TOP, "warosc", Topic_Value)
			token = mclient.Publish(TOP, byte(0), false, Topic_Value)
			if token.Wait() && token.Error() != nil {
				fmt.Printf("Fail to publish, %v", token.Error())
			}

		}

	}

}
