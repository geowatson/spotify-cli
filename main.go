package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type CommandFunc func(f *os.File) string

const BaseUrl = "https://api.spotify.com/v1"
const SecretPattern = "secret-spotify-cli-*.txt"
const ClientToken = ""

var WaitForServer = true

func getCommands() map[string]CommandFunc {
	return map[string]CommandFunc{
		"login": login,
		"next":  nextTrack,
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
		Addr:    ":8080",
		Handler: r,
	}

	_ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r.GET("/stop", func(ctx *gin.Context) {
		_ = srv.Shutdown(_ctx)
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("kek!")
		}
	}()
	time.Sleep(1 * time.Second)
	openBrowser("https://accounts.spotify.com/en/authorize" +
		"?client_id=" + ClientToken +
		"&redirect_uri=http://localhost:8080/ok" +
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
	client := &http.Client{}
	req, err := http.NewRequest("POST", BaseUrl + path, nil)
	if err != nil {
		log.Fatal("Cannot create request to connect to " + path)
	}
	req.Header.Add("Authorization", "Bearer " + token)
	response, err := client.Do(req)
	if err != nil {
		return "Cannot move to the next song :("
	}
	if response.StatusCode < 300 {
		return "Playing next"
	} else if response.StatusCode == http.StatusUnauthorized {
		return "You need to re-login."
	}
	return "Cannot move to the next song :("
}

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
	return string(content), nil
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
			println(v(file))
			return
		}
	}
	println("Command not found; available are:")
	for k := range getCommands() {
		println("    " + k)
	}
	println()
}

func main() {
	if len(os.Args) < 2 {
		println("Enter command. pleas")
		return
	}
	args := os.Args[1:]
	processCommand(args)
}
