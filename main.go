package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type CommandFunc func(f *os.File) string
type Devices struct {
	Devices []Device
}
type Device struct {
	Id string `json:"id"`
	Name string `json:"name"`
	IsActive bool `json:"is_active"`
}

type FeaturedPlaylistsResponse struct {
	Message string `json:"message"`
	Playlists Playlists `json:"playlists"`
}

type Playlists struct {
	Items []Playlist
	Total int
}

type Playlist struct {
	Id string `json:"id"`
	Name string `json:"name"`
	Description string `json:"description"`
}

const ServletPort = "7911"
const ClientToken = ""  // can be retrieved from https://developer.spotify.com/dashboard/applications
const BaseUrl = "https://api.spotify.com/v1"
const SecretPattern = "secret-spotify-cli-*.txt"


var WaitForServer = true
var CurrentToken string

func getCommands() map[string]CommandFunc {
	return map[string]CommandFunc{
		"login": login,
		"next":  nextTrack,
		"device": selectDevice,
		"random": playRandomSong,
	}
}

func login(file *os.File) string {
	println("Prompting authorization page...")
	WaitForServer = true
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Static("/ok", "./static")
	r.GET("/", func(ctx *gin.Context) {
		token := ctx.Query("access_token")
		_, err := file.Write([]byte(token))
		if err != nil {
			log.Fatal(err)
		}
		ctx.JSON(http.StatusOK, gin.H{
			"message": "thanks! you can close this window now.",
		})
		if len(token) > 0 {
			WaitForServer = false
		}
	})

	srv := &http.Server{
		Addr:    ":" + ServletPort,
		Handler: r,
	}

	_ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r.GET("/stop", func(ctx *gin.Context) {
		_ = srv.Shutdown(_ctx)
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	time.Sleep(1 * time.Second)
	openBrowser("https://accounts.spotify.com/en/authorize" +
		"?client_id=" + ClientToken +
		"&redirect_uri=http://localhost:" + ServletPort + "/ok" +
		"&response_type=token" +
		"&scope=ugc-image-upload user-read-playback-state user-modify-playback-state user-read-currently-playing streaming app-remote-control")

	for t := 0; t < 60; t++ {
		if WaitForServer == false {
			return "You are successfully logged in. Lets go play some music!"
		}
		time.Sleep(1 * time.Second)
	}
	return "Timeout exceeded on login"
}

func nextTrack(file *os.File) string {
	token, err := getToken(file)
	if err != nil {
		return "You need to re-login."
	}

	path := "/me/player/next"
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}

	response, err := makeRequest("POST", BaseUrl + path, headers, nil)
	if err != nil {
		return "Cannot move to the next song :("
	}

	if response.StatusCode < 300 {
		return "Playing next"
	}
	if response.StatusCode == http.StatusUnauthorized {
		return "You need to re-login."
	}
	return "Cannot move to the next song :("
}

func selectDevice(file *os.File) string {
	_, err := getToken(file)
	if err != nil {
		return "You need to log-in."
	}
	devices, err := getDevices()
	if err != nil {
		log.Fatal(err)
		return "Something bad happened while getting Devices"
	}
	if len(devices) == 0 {
		return "No available devices. Open Spotify app on any of your devices!"
	}
	if len(devices) == 1 {
		return "Currently available only \"" + devices[0].Name + "\""
	}
	println("Available devices:")
	s := ""
	for i, v := range devices {
		t := "[" + strconv.Itoa(i) + "] " + v.Name
		if v.IsActive {
			t += " (current)"
		}
		s += t + "\n"
	}
	print(s)
	print("Select device by its id (enclosed in []): ")
	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')
	text = strings.Replace(text, "\n", "", -1)
	if err != nil {
		return "Whoops! cannot read this line"
	}
	deviceId, err := strconv.Atoi(text)
	if err != nil || deviceId < 0 || deviceId > len(devices) - 1 {
		return "Malformed input"
	}
	if devices[deviceId].IsActive {
		return "Already listening on this device"
	}
	res, err := setDevice(CurrentToken, devices[deviceId].Id)
	if err != nil {
		return "Cannot change to the selected device."
	}

	return res
}

func setDevice(token string, deviceId string) (s string, err error) {
	path := "/me/player"
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	body := map[string]interface{}{
		"device_ids": []interface{}{deviceId},
		"play": true,
	}

	if res, err := makeRequest("PUT", BaseUrl + path, headers, body); err != nil || (res != nil && res.StatusCode > 399) {
		return "Couldn't change the device due to an unexpected error", err
	}

	return "Successfully changed device", nil
}

func getDevices() (devices []Device, err error) {
	path := "/me/player/devices"
	headers := map[string]string{
		"Authorization": "Bearer " + CurrentToken,
	}

	response, err := makeRequest("GET", BaseUrl + path, headers, nil)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("you need to re-login")
	}
	tempBody, _ := ioutil.ReadAll(response.Body)
	var resBody Devices
	if jsonErr := json.Unmarshal(tempBody, &resBody); jsonErr != nil {
		return nil, jsonErr
	}

	if len(resBody.Devices) == 0 {
		return nil, errors.New("no devices")
	}

	return resBody.Devices, nil
}

func playRandomSong(file *os.File) string {
	rand.Seed(time.Now().UnixNano())

	if _, err := getToken(file); err != nil {
		return "You need to log-in."
	}

	p, err := getFeaturedPlaylists()
	if err != nil {
		return "Cannot get featured playlists, reason: " + err.Error()
	}
	randPos := rand.Intn(len(p))

	if err := play("playlist", p[randPos].Id); err != nil {
		return "Cannot play random song :("
	}

	return "Playing for you now: [Playlist] " + p[randPos].Name + " - " + p[randPos].Description
}

func play(playType string, playId string) error {
	path := "/me/player/play"
	headers := map[string]string{
		"Authorization": "Bearer " + CurrentToken,
	}
	body := map[string]interface{} {
		"context_uri": "spotify:" + playType + ":" + playId,
	}

	response, err := makeRequest("PUT", BaseUrl + path, headers, body)
	if err != nil {
		log.Fatal(err)
	}
	if response.StatusCode == http.StatusNotFound {
		devices, err := getDevices()
		if err != nil {
			return errors.New("cannot get devices")
		}
		if err := startPlayOnDevice(devices[0].Id, playType, playId); err != nil {
			return errors.New("cannot get devices")
		}
		return nil
	}
	if response.StatusCode != http.StatusNoContent {
		return errors.New("play returned " + strconv.Itoa(response.StatusCode))
	}

	return nil
}

func startPlayOnDevice(deviceId string, playType string, playId string) error {
	path := "/me/player/play?device_id=" + deviceId
	headers := map[string]string{
		"Authorization": "Bearer " + CurrentToken,
	}
	body := map[string]interface{} {
		"context_uri": "spotify:" + playType + ":" + playId,
	}

	response, err := makeRequest("PUT", BaseUrl + path, headers, body)
	if err != nil {
		log.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		return errors.New("play returned " + strconv.Itoa(response.StatusCode))
	}
	if response.StatusCode == http.StatusUnauthorized {
		return errors.New("you need to re-login")
	}

	return nil
}

func getFeaturedPlaylists() (playlists []Playlist, err error) {
	path := "/browse/featured-playlists?limit=50"
	headers := map[string]string{
		"Authorization": "Bearer " + CurrentToken,
	}

	response, err := makeRequest("GET", BaseUrl + path, headers, nil)
	if err != nil {
		log.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("featured playlists returned " + strconv.Itoa(response.StatusCode))
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("you need to re-login")
	}

	tempBody, _ := ioutil.ReadAll(response.Body)
	var resBody FeaturedPlaylistsResponse
	if jsonErr := json.Unmarshal(tempBody, &resBody); jsonErr != nil {
		return nil, errors.New("cannot decode playlists")
	}

	return resBody.Playlists.Items, nil
}

/*
 * SYSTEM FUNCTIONS
 */

func openBrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		log.Fatal(err)
	}

}

func getToken(file *os.File) (token string, err error) {
	fi, err := file.Stat()
	if err != nil || fi.Size() == 0 {
		return "", errors.New("no token provided")
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		return "", err
	}
	CurrentToken = string(content)
	return CurrentToken, nil
}

func findTempFileLocation() (f string, err error) {
	matches, err := filepath.Glob(os.TempDir() + SecretPattern)
	if err != nil {
		log.Fatal("File finding failed! Ask developer to fix this.")
	}
	if len(matches) == 0 {
		return "", errors.New("cannot find temp file")
	}
	return matches[0], nil
}

func processCommand(args []string) {
	for k, v := range getCommands() {
		if args[0] == k {
			var file *os.File
			var fileErr error

			match, err := findTempFileLocation()
			if err != nil {
				file, fileErr = ioutil.TempFile(os.TempDir(), SecretPattern)
			} else {
				file, fileErr = os.OpenFile(match, os.O_RDWR, os.ModeAppend)
			}
			if fileErr != nil {
				log.Fatal(err)
			}
			responseText := v(file)
			if strings.HasSuffix(responseText, "\n") {
				print(responseText)
			} else {
				println(responseText)
			}
			return
		}
	}
	println("Command not found; available are:")
	for k := range getCommands() {
		println("    " + k)
	}
	println()
}

func makeRequest(method string, url string, headers map[string]string, body map[string]interface{}, ) (response *http.Response, err error) {
	var jsonParsed io.Reader

	if body == nil {
		jsonParsed = nil
	} else {
		jsonStr, err := json.Marshal(body)
		if err != nil {
			log.Fatal(err)
		}
		jsonParsed = bytes.NewBuffer(jsonStr)
	}

	req, err := http.NewRequest(method, url, jsonParsed)
	if err != nil {
		log.Fatal("Cannot create request with url " + url)
	}

	if headers != nil {
		for k, v := range headers {
			req.Header.Add(k, v)
		}
	}

	client := &http.Client{}
	return client.Do(req)
}

func main() {
	if len(os.Args) < 2 {
		println("Enter command. pleas")
		return
	}
	args := os.Args[1:]
	processCommand(args)
}
