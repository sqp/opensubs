package main

import (
	"github.com/sqp/opensubs"
	"flag"
	"os"
	"fmt"
	//~ "io"
	"path"
)

var dir   string
// Command line options
var langs string
var imdb  string

const usage = `OpenSubs GO API Example is a tool to download subs files.

Usage:

	%s [options] file [, file...]

Examples:

  %s -l fre,ita,eng *.avi          # Download subs in 3 languages for all avi in dir.
  %s --imdb 1234567 my_movie.mkv   # Can also try to download subs for a specific movie.
  
Without the imdb setting, we only match the movie by moviehash.

`

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, os.Args[0], os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}

	flag.StringVar(&langs, "lang", "eng", "languages separated by comma. ex: eng or eng,fre")
	flag.StringVar(&langs, "l", "eng", "see --lang")
	flag.StringVar(&imdb,  "imdb", "",    "imdb id for given file (only one file can be matched if used)")
	flag.StringVar(&imdb,  "i", "",    "see --imdb")
}

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Println("Missing file name(s)\n")
		flag.Usage()
		os.Exit(2)
	}

	get(langs, imdb, flag.Args())
}

const OPENSUBTITLE_USER_AGENT = "OS Test User Agent"

func get(langs, imdb string, files []string) error {
	// Create a new opensubs query.
	query := opensubs.NewQuery(OPENSUBTITLE_USER_AGENT)

	// Fill the query with our input.
	for _, file := range files {
		query.AddFile(file, langs) // We can search subs by moviehash.
		
		if imdb != "" {	// And we can also search subs by imdb id (both at same time).
      query.AddImdb(imdb, langs) // If we have an imdb, we can also add it to the query.
      break // only parse one file in imdb mode. 
      // This limit exist only for this example as it would be painfull to 
      // match multiple imdb id with their filenames from the command line.
      // We only stick to one imdb == one file for this version.
      // The API can search and download as many item you want at once.
		}
	}

	// At this point, no connection was started, we have build our query arguments
	// list. Now we can now ask the server for matching informations.
	//query.PrintArgs() // can be used to check your arguments before submitting.
	
	// Search matching subs info and don't forget to close the token on the server.
  if e := query.Search(); e != nil {
		return e
	}
	defer query.Logout()

	// We now have informations about available subtitles.
	query.PrintSubInfos() // Can be used to see the list of subtitles found.
	
	// Download subs files.
	byhash, byimdb := query.Get(3)

	if byhash != nil {
		for file, bylang := range byhash { // For each ref.
			basename := stripExt(file)
			for lang, list := range bylang {
				list[0].ToFile(basename + "_" + lang + ".srt") // One file is enough in moviehash mode.
				// Others aren't downloaded. The slice level here is just to get a similar
				// structure for byhash and byimdb.
				// The number of files downloaded in moviehash mode  may evolve if there
				// is needs. Feel free to ask for an API evolution.
			}
		}
	}
	
	for _, bylang := range byimdb {
		basename := stripExt(files[0])
		for lang, list := range bylang {
			for index, sub := range list {
				sub.ToFile(basename + "_" + lang + "_OS" + fmt.Sprint(index + 1) + ".srt")
			}
		}
		break // only one imdb can match
	}
	
	return nil
}


// Get filename without ext.
//
func stripExt(file string) string {
	extLen := len(path.Ext(file))
	return file[:len(file) - extLen]
}

