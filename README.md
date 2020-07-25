# Spotify CLI

Minimalist Spotify playback from CLI written in Go.

# Disclaimer

This product is currently under heavy development, use with caution! Bugs
are inevitable, feel free to report anything you found.

# Installation

Download latest from releases.

In this README we run binary from CWD, but you can install it to your 
*$PATH* using either `go install` or moving to one of your *$PATH*'s locations

# Usage

* `./spotify login` - logins you to spotify app
* `./spotify` - toggles play/pause for current playback
* `./spotify random` - play random song!
* `./spotify next` - scrobble to next song (in current random context you are in) if you are bored
* `./spotify device` - change playback device if you have more than 1

# Development

In code, you can find this line:
```go
const ClientToken = ""  // can be retrieved from https://developer.spotify.com/dashboard/applications
```
This means you need to specify token provided by Spotify Apps. Steps to get things done:

1. Create new appication
2. Specify redirect URI to the following: http://localhost:7911 (you can change port in code and use it instead)
3. Save changes
4. Retrieve your client_id from dashboard and paste it into ClientToken variable

Now you are ready to go! Try `./spotify login`