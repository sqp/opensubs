/*
Package opensubs provides searching and downloading for subtitles on opensubtitles.org using their XMLRPC API.

When matching by imdb, subs are sorted by download number.
 

Atm the output is converted from latin1 to UTF-8. I don't know if that can break other languages.

see example/example.go

	Use example : 
	
	// Create a Query. A valid user agent is required. See the opensubtitles.org links.
	query := opensubs.NewQuery(UserAgent)
	
	// Add some arguments to search, for example by imdb id.
	query.AddImdb("0066921", "eng,fre")       
	query.AddImdb("0137523", "eng,fre,ita")
	
	// and / or by moviehash.
	query.AddFile(filename, langs)
	
	// Initiate server search query.
	query.Search()
	defer query.Logout()
	
	// Download files. The argument is the number of subtitles that should be
	// downloaded for files matched in imdb mode.
	byhash, byimdb := query.Get(3)   


Using downloaded data:
First, you need to test byhash and byimdb to see if they aren't nil. There's way
too many case of errors between the download and parsing.

byhash and byimdb are map[string]map[string][]*SubInfo
 
2 levels of map[string] and 1 level of slice with those data:

 * 1st key is the source reference. Filename for byhash, and Imdb id for byimdb.
 * 2nd key is the sub language.
 * And as we can have multiple files, the slice contains those really downloaded.

Gives something like this :
	sub := byhash[filename][lang][0]

There is always at least one SubInfo in each ref/lang slice as they are created only when filled.


More usage informations could be found in 
 * the documentation :
 * the example file :

Links to usefull informations about the data source:
 * http://trac.opensubtitles.org/projects/opensubtitles/wiki/DevReadFirst
 * http://trac.opensubtitles.org/projects/opensubtitles/wiki/XMLRPC

Dependencies:
  go get code.google.com/p/go-charset/charset
  go get code.google.com/p/go-charset/data
	
API informations:
 * Consider the search API unstable yet, but it's only 4 functions, so it shouldn't hurt too much.
 * SubInfo Api should remain (at least) as is for now, unless suggestions or problems reported.

*/
package opensubs

import (
	xmlrpc "github.com/sqp/go-xmlrpc"

	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"term"
	"log"

	"os"
	"io"
	"bytes"
	"compress/gzip"
	"encoding/base64"

	"encoding/binary"

	//~ "code.google.com/p/go-charset/charset"
	//~ _ "code.google.com/p/go-charset/data"
)

// BUG(sqp): TODO: use better user agent.

// BUG(sqp): TODO: maybe reduce internal mapping between Get and download.


const OPENSUBTITLE_DOMAIN = "http://api.opensubtitles.org/xml-rpc"


//-----------------------------------------------------------------------
// Local types.
//-----------------------------------------------------------------------

// SubInfo contains informations about downloadable/downloaded subtitles.
//
// More fields can be added easily. They will be parsed directly from website.
// Just uncomment an unused field or add the one you need and it will just be
// matched like the others.
// Just make sure you leave the reader as last field as it is specifically dropped.
//
type SubInfo struct {
	MatchedBy         string
	MovieHash         string
	IDSubtitleFile    string
	SubLanguageID     string
	SubFormat         string
	//~ SubAuthorComment  string
	//~ SubHash           string
	//~ IDSubtitle        string
	SubAddDate        string
	//~ SubRating         string
	SubDownloadsCnt   string
	IDMovieImdb       string
	UserNickName      string
	UserRank          string
	//~ SubDownloadLink   string
	//~ ZipDownloadLink   string
	//~ SubtitlesLink     string
	reader            io.Reader
}

func (sub SubInfo) Id() int {
	i, _ := strconv.Atoi(sub.IDSubtitleFile)
	return i
}

func (sub SubInfo) ByHash() bool {
	return sub.MatchedBy == "moviehash"
}

func (sub SubInfo) Reader() io.Reader {
	return sub.reader
}

func (sub SubInfo) ToFile(filename string) error {
	return saveFile(filename, sub.Reader())
}



// Map SubInfo by their subId. Used to match downloaded subs.
type subIndex map[string]*SubInfo


// The list of available subs matched for one language of current reference.
// Can be sorted. Current sort method is byDownloads. More could be added easily.
type subsList []*SubInfo

func (s subsList) Len() int      { return len(s) }
func (s subsList) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// Sort method for subsList: Order subs by downloaded count. Highest is first.
type byDownloads struct{ subsList }

func (s byDownloads) Less(i, j int) bool {
	vi, _ := strconv.Atoi(s.subsList[i].SubDownloadsCnt)
	vj, _ := strconv.Atoi(s.subsList[j].SubDownloadsCnt)
	return vi > vj
}


// First level of maping.
type subByLang map[string]subsList


// Second level of maping.
type subByRef map[string]subByLang

func (byref subByRef) addSub(sub *SubInfo, key string) {
	if _, ok := byref[key]; !ok {
		byref[key] = make(subByLang)
	}
	byref[key][sub.SubLanguageID] = append(byref[key][sub.SubLanguageID], sub)
}
	



//-----------------------------------------------------------------------
// Query public API.
//-----------------------------------------------------------------------

type Query struct {
	listArgs   []interface{}
	byhash     subByRef
	byimdb     subByRef
	hashs      map[string]string // Index to rematch subs with files.
	userAgent  string
	token      string
}

func NewQuery(userAgent string) *Query {
	log.SetPrefix(term.Yellow("[OpenSubs] "))
	return &Query{
		hashs:      make(map[string]string),
		userAgent:  userAgent,
		}
}

// Chainable
func (q *Query) AddImdb(imdb, langs string) *Query {
	q.listArgs = append(q.listArgs, map[string]string{"sublanguageid": langs, "imdbid": imdb})
	return q
}


// Add a new search by moviehash. (Chainable)
//
//   filename  string                The file we need to match.
//   langs     string                The subtitles languages to find.
//
func (q *Query) AddFile(filename, langs string) *Query {
//~ log.Println("file", filename)
		if hash, e := moviehash(filename); e == nil {
			stat, _ := os.Stat(filename)
			size := fmt.Sprint(stat.Size())
			q.listArgs = append(q.listArgs, map[string]string{"sublanguageid": langs, "moviehash": hash, "moviebytesize": size})
			q.hashs[hash] = filename // Index filename on hash
		}
	
	return q
}


func (q *Query) Search() error {
	return q.search()
}


func (q *Query) Get(n int) (subByRef, subByRef) {
	var dl []string
	needed := make(subIndex)

	// Parsing list byhash. Need one file
	for _ , bylang := range q.byhash { // For each movie
		for _, list := range bylang { // For each lang
			if len(list) > 1 {warn("multiple ref for hash matched")}
			sort.Sort(byDownloads{list})
			sub := list[0]
			needed[sub.IDSubtitleFile] = sub
			dl = append(dl, sub.IDSubtitleFile)
		}
	}

//~ printSubByRef("Matched by Hash", q.byhash)

	// Parsing list byimdb to get multiple files.
	for imdb, bylang := range q.byimdb { // For each movie
		for _, list := range bylang { // For each lang
			
			sort.Sort(byDownloads{list})
			count := 0
			
			log.Println(term.Magenta("Movie found"), "  imdb:", imdb) //strconv.Itoa(imdb))
	
			for _, sub := range list { // each sub
				if n == -1 || count < n { // Unlimited or within limit: add to list.
					needed[sub.IDSubtitleFile] = sub
					dl = append(dl, sub.IDSubtitleFile)
					log.Println(term.Green(sub.SubLanguageID), sub.SubAddDate[:10], term.Yellow(sub.SubDownloadsCnt), sub.UserNickName, term.Bracket(sub.UserRank))
	
					//~ break
	
				} else {
					log.Println(term.Magenta(sub.SubLanguageID), sub.SubAddDate, sub.UserNickName, term.Bracket(sub.UserRank), term.Yellow(sub.SubDownloadsCnt))
				}
				count++
			}
		}
	}
	return q.download(dl, needed)
}


// Close the token on the server.
func (q *Query) Logout() {
	call("LogOut", q.token)
}


//-----------------------------------------------------------------------
// Server query.
//-----------------------------------------------------------------------

// Process a xmlrpc call on OpenSubtitles.org server.
func call(name string, args ...interface{}) (xmlrpc.Struct, error) {
	res, e := xmlrpc.Call(OPENSUBTITLE_DOMAIN, name, args...)
	if e == nil {
		if data, ok := res.(xmlrpc.Struct); ok {
			return data, e
		}
	}
	return nil, e
}

// Initiate connection to OpenSubtitles.org to get a valid token.
func (q *Query) connect() error {
	res, e := call("LogIn", "", "", "en", q.userAgent)
	switch {
	case e != nil:
		return e
	case res == nil || len(res) == 0:
		return errors.New("connection problem")
	}

	if token, ok := res["token"].(string); ok {
		q.token = token
		return nil
	}
	return errors.New("OpenSubtitles Token problem")
}


func (q *Query) search() error {
	e := q.connect()
	switch {
	case e != nil:
		return e
	case q.token == "":
		return errors.New("invalid token")
	}

	searchData, e := call("SearchSubtitles", q.token, q.listArgs)
	if e != nil {
		return e
	}
	for k, v := range searchData {
		if k == "data" {
			if array, ok := v.(xmlrpc.Array); ok {
				q.byhash, q.byimdb = mapSubInfos(array)
			}
		}
	}
	
	return nil
}



//~ func download(ids []string) (xmlrpc.Struct, error) {
func (q *Query) download(ids []string, needed subIndex) (subByRef, subByRef) {
	if len(ids) == 0 {
		return nil, nil
	}
	if s, e := call("DownloadSubtitles", q.token, ids); e == nil {
		for k, v := range s {
			if k == "data" {
				if array, ok := v.(xmlrpc.Array); ok { // Found valid data array.
					return q.parseSubFiles(array, needed)
				}
			}
		}
	}
	return nil, nil
}


//-----------------------------------------------------------------------
// Debug.
//-----------------------------------------------------------------------

func (q *Query) PrintArgs() {
	for _, arg := range q.listArgs {
		fmt.Printf("%# v\n", arg)
	}
}

func (q *Query) PrintSubInfos() {
	printSubByRef("Matched by Hash", q.byhash)
	printSubByRef("Matched by IMDB", q.byimdb)
}


func printSubByRef(title string, byref subByRef) {
	if len(byref) == 0 {
		return
	}
	fmt.Println(term.Underscore + title + term.Reset)
	for imdb, bylang := range byref {
		fmt.Println(term.Red("Movie ref :"), imdb)
		for lang, list := range bylang {
		fmt.Println(" ", term.Yellow(lang))
			for index, sub := range list {
				fmt.Println(" ", term.FgGreen, index, term.Reset, sub.SubAddDate[:10], term.Yellow(sub.SubDownloadsCnt), sub.UserNickName, term.Bracket(sub.UserRank))
				//~ fmt.Printf(" ", " ", "#%d : %# v\n", index,sub)
			}
		}
	}
	fmt.Println()
}
//-----------------------------------------------------------------------
// Parse downloaded files.
//-----------------------------------------------------------------------

func (q *Query) parseSubFiles(array xmlrpc.Array, needed subIndex) (subByRef, subByRef) {
	byhash := make(subByRef)
	byimdb := make(subByRef)

	var subid, subtext string
	var gz []byte
	var e error
	var reader io.Reader
	var sub *SubInfo

	for _, fi := range array {
		data, ok := fi.(xmlrpc.Struct); 
		if !ok {
			continue
		}
		subid, ok = data["idsubtitlefile"].(string)
		if !ok {
			continue
		}

		subtext, ok = data["data"].(string)
		if !ok {
			continue
		}
		
		/// Get matching SubInfo
		sub, ok = needed[subid]
		if !ok {
			continue
		}

		/// unbase64
		gz, e = base64.StdEncoding.DecodeString(subtext)
		if e != nil || len(gz) == 0 {
			warn("base64", e)
			continue
		}
		reader = bytes.NewBuffer(gz)

		/// gunzip
		reader, e =	gzip.NewReader(reader)
		if e != nil {
			warn("gunzip", e)
			continue
		}

		/// Convert to UTF-8 and save reader.
		//~ reader, e = charset.NewReader("latin1", reader)
		//~ if e != nil {
			//~ warn("utf8", e)
			//~ continue
		//~ }
		sub.reader = reader
		if sub.SubFormat != "srt" {
			warn("sub format", sub.SubFormat)
		}
		
		/// Everything was OK: add the reference to result.
		switch sub.MatchedBy {
		case "moviehash":
		//~ log.Println("got fucking file", sub.MovieHash, q.hashs[sub.MovieHash])
			byhash.addSub(sub, q.hashs[sub.MovieHash])
		case "imdbid":
			byimdb.addSub(sub, sub.IDMovieImdb)
		}
	}
if len(byhash) > 0 {
		log.Println("Found downloaded by hash : ", len(byhash))
	}


	return byhash, byimdb
}


//-----------------------------------------------------------------------
// Parse downloaded SubInfo.
//-----------------------------------------------------------------------

func mapSubInfos(data []interface{}) (subByRef, subByRef) {
	byhash := make(subByRef)
	byimdb := make(subByRef)
	
	hashImdbIndex := make(subIndex)
	var matchedImdb subsList
	for _, value := range data { // Array of data
		if vMap, ok := value.(xmlrpc.Struct); ok {

			sub := mapOneSub(vMap)
			switch sub.MatchedBy {
			case "moviehash":
				byhash.addSub(sub, sub.MovieHash)
				hashImdbIndex[sub.IDMovieImdb] = sub // saving reference for 2nd pass
			case "imdbid":
				matchedImdb = append(matchedImdb, sub)
			//~ case "tag":
			//~ case "fulltext":
			default:
				warn("match failed. not implemented", sub.MatchedBy)
			}
		}
	}
	
	for _, sub := range matchedImdb {
		if _, ok := hashImdbIndex[sub.IDMovieImdb]; !ok { // Add to imdb list only if they were not already matched by hash.
			//~ warn("sub to add to  2nd list", sub.IDMovieImdb)
			byimdb.addSub(sub, sub.IDMovieImdb)
		}
		//~ } else{warn("sub already in imdb list", sub.IDMovieImdb)}

			
	}
	return byhash, byimdb
}


func mapOneSub(parseMap map[string]interface{}) *SubInfo {
	typ := reflect.TypeOf(SubInfo{})
	n := typ.NumField()

	item := &SubInfo{}
	elem := reflect.ValueOf(item).Elem()

	for i := 0; i < n - 1; i++ { // Parsing all fields in type except last one. reader is a private member.
		field := typ.Field(i)
		if v, ok := parseMap[field.Name]; ok { // Got matching row in map
			if elem.Field(i).Kind() == reflect.TypeOf(v).Kind() { // Types are compatible.
				elem.Field(i).Set(reflect.ValueOf(v))
			} else {
				warn("XML Import Field mismatch", field.Name, elem.Field(i).Kind(), reflect.TypeOf(v).Kind())
			}
		}
	}
	return item
}


//-----------------------------------------------------------------------
// Common
//-----------------------------------------------------------------------

func saveFile(filename string, reader io.Reader) error {
	if _, e := os.Stat(filename); e == nil {
		warn("File exists ", filename)
		return errors.New("file exists")
	}

	writer, err := os.Create(filename)
	if err != nil {
		warn("Can't save ", filename, err)
		return err
	}
	warn("Write File", filename)
	defer writer.Close()
	io.Copy(writer, reader)
	return nil
}


//~ import (
	//~ "os"
	//~ "encoding/binary"
	//~ "strconv"
	//~ "flag"
//~ )

const hashBlocks = 8192

func moviehash(filename string) (string, error) {
 //~ /*
  //~ * Public Domain implementation by SQP. 
  //~ * Simply converted to Golang the C version by Kamil Dziobek.
  //~ * This code implements Gibest hash algorithm first use in Media Player Classics
  //~ * For more implementation(various languages and authors) see:
  //~ * http://trac.opensubtitles.org/projects/opensubtitles/wiki/HashSourceCodes   
  //~ */
	file, e1 := os.Open(filename) // Try to open file.
	if e1 != nil {
		return "", e1
	}
	
	stat, e2 := file.Stat() // File must have stat to get size.
	if e2 != nil {
		return "", e2
	}
	hash := uint64(stat.Size()) // Add file size to hash.

	buffer := make([]byte, hashBlocks * 8 * 2) // Two blocks buffer.
  file.Read(buffer[:hashBlocks * 8]) // Read start block.
  file.Seek(-hashBlocks * 8, 2) // Move to beginning of end block.
	file.Read(buffer[hashBlocks * 8:]) // Read end block.
	
	for i:= 0; i < hashBlocks * 2; i++ { // Parse the 2 blocks buffer with 8 bytes step.
		// Add the value of the next 8 bytes to hash.
		hash += binary.LittleEndian.Uint64(buffer[i * 8:i * 8 + 8])
	}

	//~ fmt.Printf("new hash %x\n", hash)
	//~ fmt.Println("test", strconv.FormatUint(hash, 16))
	//~ out, _ := exec.Command("moviehash.php", filename).Output()
	//~ fmt.Println("old hash", string(out))

	//~ os.Stdout.Write([]byte(strconv.FormatUint(hash, 16)))

	return strconv.FormatUint(hash, 16), nil // Return result in hexadecimal format.
}

//~ func main() {
	//~ os.Stdout.Write([]byte("hash : " + moviehash(flag.Arg(0)) + "\n"))
//~ }


/*
func moviehash_crappy(filename string) string {
	file, e := os.Open(filename)
	if e != nil {
		return ""
	}
	
	
	stat, _ := file.Stat()
	fsize := uint64(stat.Size())
  hash := []uint64{fsize & 0xFFFF, (fsize >> 16) & 0xFFFF, 0, 0}
	
	buffer := make([]byte, 65536 * 2)
  file.Read(buffer[:65536])
  file.Seek(-65536, 2)
	file.Read(buffer[65536:])
	
	block := make([]uint64, 4)
	for i:= 0; i < 8192 * 2; i++ {
		for j := 0; j < 4; j++ {
			//~ block[j], _ = binary.Uvarint(buffer[i * 8 + j * 2:i * 8 + j * 2 + 2])
			pos := i * 8 + j * 2
			block[j] = uint64(buffer[pos]) + 256 * uint64(buffer[pos + 1])
			//~ if i < 10 { fmt.Println(j, buffer[i * 8 + j * 2:i * 8 + j * 2 + 2], block[j]) }
			//~ if i < 10 { fmt.Printf("%d %x\n", j, block[j]) }
		}
			//~ if i < 10 { fmt.Println(i, "  ", block) }
		hash = AddUINT64(hash, block)
	}

	return fmt.Sprintf("%04x%04x%04x%04x", hash[3], hash[2], hash[1], hash[0])
}

func AddUINT64(hash, block []uint64) []uint64 {
	ret := make([]uint64, 4)

	carry := uint64(0);
	for i := 0; i < 4; i++ {
		if (hash[i] + block[i] + carry) > 0xffff {
			ret[i] += (hash[i] + block[i] + carry) & 0xffff;
			carry = 1;
		} else {
			ret[i] += (hash[i] + block[i] + carry);
			carry = 0;
		}
	}
	
	return ret;   
}
*/



func warn(source string, data... interface{}) {
	args := []interface{}{}
	args = append(args, term.Yellow(source))
	args = append(args, data...)
	log.Println(args...)
}

//~ func test() {
//~ search := []interface{}{
//~ xmlrpc.Struct{"sublanguageid": "eng", "imdbid": "0636287"},
//~ }
//~ xmlrpc.Show(OPENSUBTITLE_DOMAIN, "SearchSubtitles", token, search)
//~ }
