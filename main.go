package main

import (
    "bufio"
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "io/ioutil"
    "log"
    "math/rand"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "spotify/utils"
)

type command func(f *os.File) string
type handlerFunc func(w http.ResponseWriter, r *http.Request)

type Handler struct {
    Func handlerFunc
    Url  string
}

type Devices struct {
    Devices []Device
}

type Device struct {
    Id       string `json:"id"`
    Name     string `json:"name"`
    IsActive bool   `json:"is_active"`
}

type PlaylistsResponse struct {
    Message   string    `json:"message"`
    Playlists Playlists `json:"playlists"`
}

type Playlists struct {
    Items []Playlist
    Total int
}

type Playlist struct {
    Id          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description"`
}

type CategoriesResponse struct {
    Categories Categories `json:"categories"`
}

type Categories struct {
    Items []Category
}

type Category struct {
    Id   string
    Name string
}

const ServletPort = "7911"
const ClientToken = "" // can be retrieved from https://developer.spotify.com/dashboard/applications
const BaseUrl = "https://api.spotify.com/v1"
const SecretPattern = "secret-spotify-cli-*.txt"

// I'm very sorry for this; I feel really disappointed about it too but there is no actual way to convert
// static files to binary while building. We will change this in future, I promise!
const LoginRedirectPage = `
    <div id="message"></div>
    <script>
        let doc = document.getElementById("message")
        let data = window.location.hash.substr(1).split("&").forEach(v => {
            let slice = v.split("=")
            if (slice[0] === "access_token") {
                fetch("http://localhost:` + ServletPort + `/token/store?access_token=" + slice[1]).then(r => {
                    if (r.status === 200) doc.innerHTML = "Success! You can close this window and use CLI."
                    else doc.innerHTML = "Cannot connect to the local server. Rerun login command."
                })
            } else doc.innerHTML = "No access token were provided from Spotify."
        })
    </script> 
`

var CurrentToken string

func getCommands() map[string]command {
    return map[string]command{
        "login":  login,
        "next":   nextTrack,
        "device": selectDevice,
        "random": playRandomSong,
    }
}

func login(file *os.File) string {
    var blocked = true
    var badToken = false
    timeout := 30 * time.Second
    port := ServletPort
    handlers := []Handler{
        {
            Url: "/token/store",
            Func: func(w http.ResponseWriter, r *http.Request) {
                keys, ok := r.URL.Query()["access_token"]
                if !ok || len(keys) == 0 {
                    log.Fatal("No access token provided from Spotify... what a shame!")
                }
                _, err := file.Write([]byte(keys[0]))
                if err != nil {
                    log.Fatal("Cannot save token for some reason :(")
                }
                blocked = false
                if len(keys[0]) == 0 {
                    badToken = true
                }
                _, _ = fmt.Fprintf(w, "Thanks!")
            },
        },
        {
            Url: "/health",
            Func: func(w http.ResponseWriter, r *http.Request) {
                _, _ = fmt.Fprintf(w, "im alive!")
            },
        },
        {
            Url: "/ok/",
            Func: func(w http.ResponseWriter, r *http.Request) {
                _, _ = fmt.Fprintf(w, LoginRedirectPage)
            },
        },
    }
    go servlet(port, handlers)

    println("Waiting for server to start...")
    if err := watcher(timeout, "http://localhost:"+port+"/health"); err != nil {
        log.Fatal("Server could not be started in " + strconv.Itoa(int(timeout.Seconds())) + " seconds :(")
    }

    println("Server started! Opening authentication page in browser...")
    baseUrl := "https://accounts.spotify.com/en/authorize"
    query := "?client_id=" + ClientToken +
        "&redirect_uri=http://localhost:" + ServletPort + "/ok" +
        "&response_type=token" +
        "&scope=ugc-image-upload user-read-playback-state user-modify-playback-state user-read-currently-playing streaming app-remote-control"
    fullUrl := baseUrl + query
    time.Sleep(600 * time.Millisecond)
    if opened := browser.Open(fullUrl); !opened {
        println("Cannot open browser :(")
        println("Please open this link in your browser: " + baseUrl + strings.Replace(query, " ", "%20", -1))
    }

    if err := waiter(2*timeout, &blocked); err != nil {
        return "Timeout of total " + strconv.Itoa(int((2 * timeout).Seconds())) + " seconds reached on authorization."
    }

    if badToken {
        return "Bad token received :("
    }

    return "You are successfully logged in. Lets go play some music!"
}

func servlet(port string, handlers []Handler) {
    for _, h := range handlers {
        http.HandleFunc(h.Url, h.Func)
    }
    _ = http.ListenAndServe(":"+port, nil)
}

func watcher(t time.Duration, addr string) error {
    for i := 0; i < int(t.Seconds()); i++ {
        time.Sleep(1 * time.Second)
        res, err := http.Get(addr)
        if err != nil {
        } // ignore err
        if res != nil && res.StatusCode == 200 {
            return nil
        }
    }
    return errors.New("server did not start")
}

func waiter(t time.Duration, blocked *bool) error {
    for i := 0; i < int(t.Seconds()); i++ {
        time.Sleep(1 * time.Second)
        if !*blocked {
            return nil
        }
    }
    return errors.New("time exceeded")
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

    response, err := makeRequest("POST", BaseUrl+path, headers, nil)
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
    if err != nil || deviceId < 0 || deviceId > len(devices)-1 {
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
        "play":       true,
    }

    if res, err := makeRequest("PUT", BaseUrl+path, headers, body); err != nil || (res != nil && res.StatusCode > 399) {
        return "Couldn't change the device due to an unexpected error", err
    }

    return "Successfully changed device", nil
}

func getDevices() (devices []Device, err error) {
    path := "/me/player/devices"
    headers := map[string]string{
        "Authorization": "Bearer " + CurrentToken,
    }

    response, err := makeRequest("GET", BaseUrl+path, headers, nil)
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

// we dont need this right now
//func playRandomSong(file *os.File) string {
//	rand.Seed(time.Now().UnixNano())
//
//	if _, err := getToken(file); err != nil {
//		return "You need to log-in."
//	}
//
//	p, err := getFeaturedPlaylists()
//	if err != nil {
//		return "Cannot get featured playlists, reason: " + err.Error()
//	}
//	randPos := rand.Intn(len(p))
//
//	if err := play("playlist", p[randPos].Id); err != nil {
//		return "Cannot play random song :("
//	}
//
//	return "Playing for you now: [Playlist] " + p[randPos].Name + " - " + p[randPos].Description
//}

func playRandomSong(file *os.File) string {
    rand.Seed(time.Now().UnixNano())

    if _, err := getToken(file); err != nil {
        return "You need to log-in."
    }

    c, err := getCategories()
    if err != nil {
        log.Fatal("Cannot get categories")
    }
    randCategoryPos := rand.Intn(len(c))

    p, err := getCategoryPlaylists(c[randCategoryPos].Id)
    if err != nil {
        return "Cannot get category playlists, reason: " + err.Error()
    }
    randPlaylistPos := rand.Intn(len(p))

    if err := play("playlist", p[randPlaylistPos].Id); err != nil {
        return "Cannot play random song :("
    }

    return "Playing for you now: [" + c[randCategoryPos].Name + "] " + p[randPlaylistPos].Name + " - " + p[randPlaylistPos].Description
}

func play(playType string, playId string) error {
    path := "/me/player/play"
    headers := map[string]string{
        "Authorization": "Bearer " + CurrentToken,
    }
    body := map[string]interface{}{
        "context_uri": "spotify:" + playType + ":" + playId,
    }

    response, err := makeRequest("PUT", BaseUrl+path, headers, body)
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

func pause() error {
    path := "/me/player/pause"
    headers := map[string]string{
        "Authorization": "Bearer " + CurrentToken,
    }

    response, err := makeRequest("PUT", BaseUrl+path, headers, nil)
    if err != nil {
        log.Fatal(err)
    }
    if response.StatusCode != http.StatusNoContent {
        return errors.New("cannot pause")
    }

    return nil
}

// togglePlay is a boilerplate for play() func
// needs to be replaced with more generalized func
func togglePlay(file *os.File) string {
    if _, err := getToken(file); err != nil {
        return "You need to log-in."
    }

    path := "/me/player/play"
    headers := map[string]string{
        "Authorization": "Bearer " + CurrentToken,
    }
    if errPause := pause(); errPause == nil {
        return "Paused playback"
    }

    response, err := makeRequest("PUT", BaseUrl+path, headers, nil)
    if err != nil {
        log.Fatal(err)
    }
    if response.StatusCode == http.StatusNotFound {
        devices, err := getDevices()
        if err != nil {
            return "Cannot get devices"
        }
        if len(devices) == 0 {
            return "No devices are running. Start Spotify on one of them."
        }
    }
    if response.StatusCode != http.StatusNoContent {
        return "Play returned " + strconv.Itoa(response.StatusCode)
    }

    return "Resumed playback"
}

func getCategoryPlaylists(categoryId string) (playlists []Playlist, err error) {
    path := "/browse/categories/" + categoryId + "/playlists?limit=50"
    headers := map[string]string{
        "Authorization": "Bearer " + CurrentToken,
    }

    response, err := makeRequest("GET", BaseUrl+path, headers, nil)
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
    var resBody PlaylistsResponse
    if jsonErr := json.Unmarshal(tempBody, &resBody); jsonErr != nil {
        return nil, errors.New("cannot decode playlists")
    }

    return resBody.Playlists.Items, nil
}

func getCategories() (categories []Category, err error) {
    path := "/browse/categories?limit=50"
    headers := map[string]string{
        "Authorization": "Bearer " + CurrentToken,
    }

    response, err := makeRequest("GET", BaseUrl+path, headers, nil)
    if err != nil {
        log.Fatal(err)
    }
    if response.StatusCode != http.StatusOK {
        log.Fatal("Cannot get categories, got code " + strconv.Itoa(response.StatusCode))
    }

    tempBody, _ := ioutil.ReadAll(response.Body)
    var resBody CategoriesResponse
    if jsonErr := json.Unmarshal(tempBody, &resBody); jsonErr != nil {
        log.Fatal("Cannot decode categories")
    }

    return resBody.Categories.Items, nil
}

func startPlayOnDevice(deviceId string, playType string, playId string) error {
    path := "/me/player/play?device_id=" + deviceId
    headers := map[string]string{
        "Authorization": "Bearer " + CurrentToken,
    }
    body := map[string]interface{}{
        "context_uri": "spotify:" + playType + ":" + playId,
    }

    response, err := makeRequest("PUT", BaseUrl+path, headers, body)
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

// we dont need this right now
//func getFeaturedPlaylists() (playlists []Playlist, err error) {
//    path := "/browse/featured-playlists?limit=50"
//    headers := map[string]string{
//        "Authorization": "Bearer " + CurrentToken,
//    }
//
//    response, err := makeRequest("GET", BaseUrl+path, headers, nil)
//    if err != nil {
//        log.Fatal(err)
//    }
//    if response.StatusCode != http.StatusOK {
//        return nil, errors.New("featured playlists returned " + strconv.Itoa(response.StatusCode))
//    }
//    if response.StatusCode == http.StatusUnauthorized {
//        return nil, errors.New("you need to re-login")
//    }
//
//    tempBody, _ := ioutil.ReadAll(response.Body)
//    var resBody PlaylistsResponse
//    if jsonErr := json.Unmarshal(tempBody, &resBody); jsonErr != nil {
//        return nil, errors.New("cannot decode playlists")
//    }
//
//    return resBody.Playlists.Items, nil
//}

/*
 * SYSTEM FUNCTIONS
 */

// getToken is a system handler function that receives token from default storage that
// is set by os.TempDir()/secretPattern. Returns nil otherwise.
//
// Used in cmd handler functions
//
// Does not guarantee that token is still alive (or refreshed)
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

func openTempFile() *os.File {
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
    return file
}

func processCommand(args []string) {
    if len(args) == 0 {
        file := openTempFile()
        println(togglePlay(file))
        return
    }
    for k, v := range getCommands() {
        if args[0] == k {
            file := openTempFile()
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
    args := os.Args[1:]
    processCommand(args)
}
