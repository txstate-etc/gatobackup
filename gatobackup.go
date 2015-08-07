package main

import (
	"runtime"
	"flag"
	"log"
	"sync"
	"strconv"
	"strings"
	"hash/fnv"
	"bufio"
	"time"
	"io"
	"os"
	"errors"
)

// Global variables filled by flag module
var (
	sessionSplitHash bool
	sessionGroupSize int
	sessionQueueSize int
	dirBase string
	stamp string
	cwd string
	eSave *log.Logger // Used to save out backup errors
)

func init() {
	flag.BoolVar(&sessionSplitHash, "split", false, "Assign a channel per session and hash the node name to determine on which channel to place the node on. This is only required for multiple publics as activations of the same site will generate different UUIDs on each public; thus producing different dumps even though the displayed content is the same. The default is to have one channel that all session threads pull work from.")
	flag.IntVar(&sessionGroupSize, "groupsize", 2, "Number of connections per session. The default is 2.")
	flag.IntVar(&sessionQueueSize, "queuesize", 1000, "Sets the queue size for each channel. The default is 1000.")
	flag.StringVar(&stamp, "stamp", "", "Append a backup batch extension to the name of each exported file. This is used for systems that do not manage their own versions such as Data Domain. Default is to not include a stamp.")
	flag.StringVar(&dirBase, "workdir", "", "Parent directory where the registry directory (which stores the hash dumps within sub-directories for each repo), data directory (which stores the backed up versions within sub-directories for each repo), and log directory. This directory may also be used as a temporary working directory to record the backup progress. The default is to use the current working directory.")
	var errInit error
	cwd, errInit = os.Getwd()
	if errInit != nil {
		log.Fatal(errInit)
	}
}

// Separate out file handling to only use io.Writers
// this allows us to change out these functions
// for testing

// Return a writer that is associated with a file
// within our datastore for the node.
var openStore func (n *Node) (io.WriteCloser, error)

// Get hash string for hash of node dump if the
// associated file exists or return empty string
// if the file does not exist.
var getHash func (n *Node) (string, error)

// Create file and store hash string generated
// from the node dump within it.
var putHash func (n *Node, hash string) error

type Backup chan *Node

// For each node pulled off the Backup channel
// Check hash of dump of the node, and if it
// has changed then export the node and save
// the contents out to a file. Then update
// the hash of dump file.
func (b Backup) run(wg *sync.WaitGroup, s *Session, thread int) {
	defer wg.Done()
	for node := range b {
		// If we get an error such as no previous version
		// of hash file exists we will use the empty string
		// returned in this case.
		hashSaved, _ := getHash(node)
		//if err != nil {
			// log.Printf("ERROR: [%d] %s -- unable to obtain saved hash of dump -- %s", thread, node, err.Error())
			// keep processing node as error may just be due to file did not exist.
			// as hashSaved is an empty string this means that we will backup the node.
		//}
		// Start timer for generating hash of dump.
		startDump := time.Now()
		hashDump, err := s.hashDump(node)
		if err != nil {
			eSave.Printf("[%d] %s -- unable to generate hash dump -- %s", thread, node, err.Error())
			log.Printf("ERROR: [%d] %s -- unable to generate hash dump -- %s", thread, node, err.Error())
			continue
		}
		elapseDump := time.Since(startDump)
		// If hashes match up then content for node has
		// not changed, and no backup is required.
		if hashDump == hashSaved {
			log.Printf("INFO: [%d] %s dump(%s) Unchanged.", thread, node, elapseDump)
			continue
		}
		// Start timer for saving node content.
		startSave := time.Now()
		w, err := openStore(node)
		if err != nil {
			eSave.Printf("[%d] %s -- unable to open node file within datastore for backup -- %s", thread, node, err.Error())
			log.Printf("ERROR: [%d] %s dump(%s) -- unable to open node file within datastore for backup -- %s", thread, node, elapseDump, err.Error())
			continue
		}
		err = s.saveNode(node, w)
		err2 := w.Close()
		if err != nil {
			// TODO: Remove backup file as it may contain incomplete or corrupt data -- either partial data or empty
			eSave.Printf("[%d] %s -- unable to save out node to file within datastore -- %s", thread, node, err.Error())
			log.Printf("ERROR: [%d] %s dump(%s) -- unable to save out node to file within datastore -- %s", thread, node, elapseDump, err.Error())
			continue
		}
		if err2 != nil {
			// TODO: Remove backup file as it contains bad data -- either partial data or empty
			eSave.Printf("[%d] %s -- unable to close node file within datastore -- %s", thread, node, err2.Error())
			log.Printf("ERROR: [%d] %s dump(%s) -- unable to close node file within datastore -- %s", thread, node, elapseDump, err2.Error())
			continue
		}
		elapseSave := time.Since(startSave)
		log.Printf("INFO: [%d] %s dump(%s) save(%s)", thread, node, elapseDump, elapseSave)
		// Save current hash to file; so we will not
		// backup until the content changes again.
		err = putHash(node, hashDump)
		if err != nil {
			// Note that saving backup file was successfull,
			// but we may end up backing the file next time
			// as we were not able to register this hash.
			log.Printf("WARNING: [%d] %s -- unable to save hash to file - During next backup process file may be backed up again -- %s ", thread, node, err.Error())
			continue
		}
	}
}

// Setup session queues
// sss is a list of unfiltered session straight from
//   parameters which may contain empty strings
// ss is a list of sessions
// ssh sessionSplitHash bool
// sgs sessionGroupSize int
// sqs sessionQueueSize int
// qNode func(string) function sends a list of names
//   of nodes to the queue for processing.
// qClose func() -- function to close channels in
//   queue and waits to return until they are done
//   processing.
func queue(sss []string, ssh bool, sgs, sqs int) (qNode func(*Node), qClose func(), err error) {
	if sgs < 1 {
		err = errors.New("groupsize parameter must be at least of size 1, and not " + strconv.Itoa(sgs))
		return
	}
	if sqs < 0 {
		err = errors.New("queuesize parameter must be at least of size 0, and not " + strconv.Itoa(sqs))
		return
	}
	var ss []*Session
	for _, us := range sss {
		var s *Session
		if s, err = NewSession(us); err != nil {
			// Majority of issues is that we have
			// an empty session that gets included
			// if extra spacing is included in the
			// parameters; for such errors we just
			// want to skip. At some point we may
			// want to check for bad sessions and
			// urls and actually error out.
			continue
		}
		ss = append(ss, s)
	}
	nss := len(ss)
	if nss < 1 {
		err = errors.New("Backup requires at least one 'url,session' parameter to run.")
		return
	}
	// Setup concurrent backup processes with sessions
	var wg sync.WaitGroup
	wg.Add(nss * sgs)
	if ssh {
		bus := make([]Backup, nss)
		for idx, s := range ss {
			bu := make(Backup, sqs)
			bus[idx] = bu
			for i := 0; i < sgs; i++ {
				go bu.run(&wg, s, (idx * sgs + i))
			}
		}
		// mod of hash node name.
		// backups[hash(node) % nss] <- node
		qNode = func (n *Node) {
			h := fnv.New64()
			h.Write([]byte(n.String()))
			bus[int(h.Sum64() % uint64(nss))] <- n
		}
		qClose = func() {
			for _, q := range bus {
				close(q)
			}
			wg.Wait()
		}
	} else {
		bu := make(Backup, sqs)
		for idx, s := range ss {
			for i := 0; i < sgs; i++ {
				go bu.run(&wg, s, (idx * sgs + i))
			}
		}
		qNode = func (n *Node) {
			bu <- n
		}
		qClose = func() {
			close(bu)
			wg.Wait()
		}
	}
	return
}

func genPath(n *Node, base, ext string) string {
	return base + "/" + n.Repo + "/" + n.Repo + "." + n.Name + ext
}

// WARNING: It is up to the process calling the function to close the writer.
func openStoreFunc(dir, stamp string) (func (n *Node) (io.WriteCloser, error)) {
	return func (n *Node) (io.WriteCloser, error) {
		return os.Create(genPath(n, dir, ".xml" + stamp))
	}
}

// Will return an empty string if unsuccessful at retrieving the saved hash.
func getHashFunc(dir string) (func (n *Node) (string, error)) {
	return func (n *Node) (string, error) {
		f, err := os.Open(genPath(n, dir, ".xml.sha1"))
		if err != nil {
			// TODO: may wish to determine if the file did not previously exist
			return "", err
		}
		defer f.Close()
		// Read only 40 byte sha1 hash
		data := make ([]byte, 40)
		count, _ := f.Read(data)
		// Return empty string instead of partial hash
		if count != 40 {
			return "", nil
		}
		return string(data), nil
	}
}

func putHashFunc(dir string) (func (n *Node, hash string) error) {
	return func (n *Node, hash string) (err error) {
		// Do NOT write out empty hash string
		if hash == "" {
			return nil
		}
		var f *os.File
		f, err = os.Create(genPath(n, dir, ".xml.sha1"))
		if err != nil {
			return
		}
		defer func() {
			if err != nil {
				err = f.Close()
			} else {
				f.Close()
			}
		}()
		// Mimic output of sha1sum function that appends
		// "  -\n" (without filename) when content is
		// piped to the command.
		_, err = f.Write([]byte(hash  + "  -\n"))
		return
	}
}

// Sanitize Global Flag Values for
// file extension stamps and directory locations
func sanitizeFileAndDirFlagValues() {
	if dirBase == "" {
		// If dirBase is empty then make it the current working directory
		dirBase = cwd
	} else if !strings.HasPrefix(dirBase, "/") {
		// If dirBase is a relative path then add current working directory
		dirBase = cwd + "/" + dirBase
	}
	// If stamp is a non-empty string then make sure it is pre-appended with a dot
	if stamp != "" && (!strings.HasPrefix(stamp, ".")) {
		stamp = "." + stamp
	}
}

func saveErrors(name string) (*log.Logger, error) {
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	return log.New(f, "ERROR: ", log.LstdFlags), nil
}

// Example usage:
// # Setup environment
// cd $HOME/staging/edit/
// mkdir -p data/{config,dms,removed,usergroups,userroles,users,website}
// ln -s /mnt/nfs/versions/gato/staging/edit/ versions
// mkdir -p versions/{config,dms,removed,usergroups,userroles,users,website}
// # then run within new directory
// echo -e 'website,testing\nusers,admin.testing' | ./gatobackup --split --stamp=2015-07-31 url1,session1 url2,session2
func main() {
	// Remove GOMAXPROCS in golang 1.5
	runtime.GOMAXPROCS(runtime.NumCPU())

	flag.Parse()
	sanitizeFileAndDirFlagValues()

	// Setup logging to maintain failed save nodes.
	var err error
	eSave, err = saveErrors(dirBase + "/save.failed")
	if err != nil {
		log.Fatal("ERROR: not able to save errors in 'save.failed' file -- ", err)
	}

	// Get functions
	openStore = openStoreFunc(dirBase + "/data", stamp)
	getHash = getHashFunc(dirBase + "/registry")
	putHash = putHashFunc(dirBase + "/registry")
	qNode, qClose, err := queue(flag.Args(), sessionSplitHash, sessionGroupSize, sessionQueueSize)
	if err != nil {
		log.Fatal("ERROR: [main] Unable to setup session channel(s) -- ", err)
	}

	// After this point failures are viewed as individual
	// issues per node and not total failure of process.
	log.Println("INFO: Backup started")

	// Populate Backup channel with names of nodes supplied from standard input.
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		n, err := NewNode(strings.TrimSpace(in.Text()))
		if err != nil {
			eSave.Printf("[main] Unable to parse -- %s", err.Error())
			log.Printf("ERROR: [main] Unable to parse -- %s", err.Error())
			continue
		}
		qNode(n)
	}
	qClose()

	log.Println("INFO: Backup finished")
}
