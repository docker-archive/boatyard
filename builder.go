package main

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ant0ine/go-json-rest/rest"
	"github.com/garyburd/redigo/redis"
	"github.com/nu7hatch/gouuid"
	"io/ioutil"
	"bufio"
	"log"
	"net/http"
	"os"
	"strings"
	"net/http/httputil"
	"net"
	"io"
)

type PassedParams struct {
	Image_name string
	Username   string
	Password   string
	Email      string
	Dockerfile string
	ZipUrl 	   string
}

type PushAuth struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Serveraddress string `json:"serveraddress"`
	Email         string `json:"email"`
}

type JobID struct {
	JobIdentifier string
}

type JobStatus struct {
	Status string
}

type JobLogs struct {
	Logs string
}

type StreamCatcher struct {
	Stream      string `json:"stream"`
	ErrorDetail string `json:"errorDetail"`
}

func main() {

	handler := rest.ResourceHandler{
		EnableRelaxedContentType: true,
	}

	handler.SetRoutes(
		&rest.Route{"POST", "/api/v1/build", BuildImageFromDockerfile},
		&rest.Route{"GET", "/api/v1/:jobid/status", GetStatusForJobID},
		&rest.Route{"GET", "/api/v1/:jobid/logs", GetLogsForJobID},
	)

	//Use the environment variable PORT, else default.
	var port string
	if os.Getenv("PORT") != "" {
		port = os.Getenv("PORT")
	} else {
		port = ":8080"
	}
	http.ListenAndServe(port, &handler)

}

//3 steps.  Unpack the jobId that came in.  Open the redis connection and get the right status.  Then write the status back.
func GetStatusForJobID(w rest.ResponseWriter, r *rest.Request) {
	jobid := r.PathParam("jobid")

	//Open a redis connection.  c is type redis.Conn
	c := RedisConnection()
	defer c.Close()

	var status JobStatus
	var err error
	status.Status, err = redis.String(c.Do("HGET", jobid, "status"))
	if err != nil {
		log.Fatal(err)
	}
	w.WriteJson(status)
}

//3 steps.  Unpack the jobId that came in.  Open the redis connection and get the logs.  Then write the logs back.
func GetLogsForJobID(w rest.ResponseWriter, r *rest.Request) {
	jobid := r.PathParam("jobid")

	//Open a redis connection.  c is type redis.Conn
	c := RedisConnection()
	defer c.Close()

	var logs JobLogs
	var err error
	logs.Logs, err = redis.String(c.Do("HGET", jobid, "logs"))
	if err != nil {
		log.Fatal(err)
	}
	w.WriteJson(logs)
}

//Builds the image in a docker node.
func BuildImageFromDockerfile(w rest.ResponseWriter, r *rest.Request) {

	//Unpack the params that come in.
	passedParams := PassedParams{}
	err := r.DecodeJsonPayload(&passedParams)
	if err != nil {
		rest.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	//Create a uuid for the specific job.
	var jobid JobID
	jobid.JobIdentifier = JobUUIDString()

	//Open a redis connection.  c is type redis.Conn
	c := RedisConnection()
	defer c.Close()

	//Set the status to building in the cache.
	c.Do("HSET", jobid.JobIdentifier, "status", "Building")

	//Write the jobid back and flush the buffer.
	w.WriteJson(jobid)
	w.(http.ResponseWriter).Write([]byte("\n"))
	w.(http.Flusher).Flush()

	//Parse the image name if it has a . in it.  Differentiate between private and docker repos.
	//Will cut quay.io/ichaboddee/ubuntu into quay.io AND ichaboddee/ubuntu.
	//If there is no . in it, then splitImageName[0-1] will be nil.  Code relies on that for logic later.
	splitImageName := make([]string, 2)
	if strings.Contains(passedParams.Image_name, ".") {
		splitImageName = strings.SplitN(passedParams.Image_name, "/", 2)
	}

	//Create the post request to build.  Query Param t=image name is the tag.
	buildUrl := ("/v1.10/build?t=" + passedParams.Image_name)

	//Open connection to docker and build.  The request will depend on whether a dockerfile was passed or a url to a zip.
	dockerDial := Dial()
	buildConnection := httputil.NewClientConn(dockerDial, nil)
	buildReq, err := http.NewRequest("POST", buildUrl, ReaderForInputType(passedParams))
    buildResponse, err := buildConnection.Do(buildReq)
	buildReader := bufio.NewReader(buildResponse.Body)

	fmt.Printf(buildResponse.Status)

	var logsString string
	//Loop through.  If stream is there append it to the buildLogs string and update the cache.
	for {
		//Breaks when there is nothing left to read.
		line, err := buildReader.ReadBytes('\r')
		if err != nil {
			break
		}
		line = bytes.TrimSpace(line)

		//Unmarshal the json in to my structure.
		var stream StreamCatcher
		err = json.Unmarshal(line, &stream)
		//This if catches the error from docker and puts it in logs in the cache, then fails.
		if stream.ErrorDetail != "" {
			buildLogsSlice := []byte(logsString)
			buildLogsSlice = append(buildLogsSlice, []byte(stream.ErrorDetail)...)
			logsString = string(buildLogsSlice)
			c.Do("HSET", jobid.JobIdentifier, "logs", logsString)
			log.Fatal(stream.ErrorDetail)
		}

		if stream.Stream != "" {
			buildLogsSlice := []byte(logsString)
			buildLogsSlice = append(buildLogsSlice, []byte(stream.Stream)...)
			logsString = string(buildLogsSlice)
			c.Do("HSET", jobid.JobIdentifier, "logs", logsString)
		}
	}
	
	//Update status in the cache, then start the push process.
	c.Do("HSET", jobid.JobIdentifier, "status", "Pushing")

	pushUrl := ("/v1.10/images/" + passedParams.Image_name + "/push")
	pushConnection := httputil.NewClientConn(dockerDial, nil)
	pushReq, err := http.NewRequest("POST", pushUrl, nil)
	pushReq.Header.Add("X-Registry-Auth", StringEncAuth(passedParams, ServerAddress(splitImageName[0])))
    pushResponse, err := pushConnection.Do(pushReq)
	pushReader := bufio.NewReader(pushResponse.Body)

	//Loop through.  Only concerned with catching the error.
	for {
		//Breaks when there is nothing left to read.
		line, err := pushReader.ReadBytes('\r')
		if err != nil {
			break
		}
		line = bytes.TrimSpace(line)

		//Unmarshal the json in to my structure.
		var stream StreamCatcher
		err = json.Unmarshal(line, &stream)

		//This if catches the error from docker and puts it in logs in the cache, then fails.
		if stream.ErrorDetail != "" {
			pushLogsSlice := []byte(logsString)
			pushLogsSlice = append(pushLogsSlice, []byte(stream.ErrorDetail)...)
			logsString = string(pushLogsSlice)
			c.Do("HSET", jobid.JobIdentifier, "logs", logsString)
			log.Fatal(stream.ErrorDetail)
		}
	}

	//Finished.  Update status in the cache.
	c.Do("HSET", jobid.JobIdentifier, "status", "Finished")

}

//String encode the info required for X-AUTH.  Username, Password, Email, Serveraddress.
func StringEncAuth(passedParams PassedParams, serveraddress string) string {
	//Encoder the needed data to pass as the X-RegistryAuth Header
	var data PushAuth
	data.Username = passedParams.Username
	data.Password = passedParams.Password
	data.Email = passedParams.Email
	data.Serveraddress = serveraddress

	jsonData, err := json.Marshal(data)
	if err != nil {
		fmt.Println("error:", err)
	}
	sEnc := base64.StdEncoding.EncodeToString([]byte(jsonData))
	return sEnc
}

//Essentially docker_node := os.Getenv("DOCKER_NODE") | default_node
func DockerNode() string {

	var docker_node string
	if os.Getenv("DOCKER_HOST") != "" {
		docker_node = os.Getenv("DOCKER_HOST")
		// docker_node = "http://localhost:4243"
		fmt.Println(docker_node + "is the DOCKER NODE *****^^^^^((((((")
	} else {
		docker_node = "http://127.0.0.1:4243"
	}
	// fmt.Println(docker_node + "is the docker node ^^^^")
	return docker_node
}


func Dial() net.Conn {
	var docker_host string
	if os.Getenv("DOCKER_HOST") != "" {
		dockerHost := os.Getenv("DOCKER_HOST")
		splitStrings := strings.SplitN(dockerHost, "//", 2)
		docker_host = splitStrings[1]
		// fmt.Println(docker_host + "^^^^^ should be loclahost:4243    ((((((")
	} else {
		docker_host = "localhost:4243"
	}

	dockerDial, err := net.Dial("tcp", docker_host)
	if err != nil {
		log.Fatal(err)
	}

	return dockerDial
}

//Open a redis connection.
func RedisConnection() redis.Conn {
	c, err := redis.Dial("tcp", CachePort())
	if err != nil {
		log.Fatal(err)
	}
	// fmt.Println("Made it to returning the redis connection")
	return c
}

//create a Unique JobID and return it as a string.
func JobUUIDString() string {
	uniqueJobId, uuidErr := uuid.NewV4()
	if uuidErr != nil {
		log.Fatal(uuidErr)
	}
	s := uniqueJobId.String()
	// fmt.Println(s + "Made it to the end of the JobUUID FUNCT")
	return s

}

//Env variables can be used to set the CACHE_PORT
func CachePort() string {

	var cacheTCPAddress string
	if os.Getenv("CACHE_1_PORT_6379_TCP_ADDR") != "" {
		cacheTCPAddress = os.Getenv("CACHE_1_PORT_6379_TCP_ADDR")
	} else {
		cacheTCPAddress = ""
	}

	var cachePort string
	if os.Getenv("CACHE_1_PORT_6379_TCP_PORT") != "" {
		cachePort = os.Getenv("CACHE_1_PORT_6379_TCP_PORT")
	} else {
		cachePort = "6379"
	}
	// fmt.Println(cacheTCPAddress + ":" + cachePort + "is the CachePort Return")
	return cacheTCPAddress + ":" + cachePort
}

func ServerAddress(privateRepo string) string {

	//The server address is different for a private repo. *******^&%^*&^*%&********
	fmt.Println(privateRepo + "right before the IFSTATEMENT")
	var serveraddress string
	if privateRepo != "" {
		serveraddress = ("https://" + privateRepo + "/v1/")
	} else {
		serveraddress = "https://index.docker.io/v1/"
	}
	// fmt.Println(serveraddress + "is the SERVERADDRESSSSS")
	return serveraddress

}

//Reader will read from either the zip made from the dockerfile passed in or the zip from the url passed in.
func ReaderForInputType(passedParams PassedParams) io.Reader {
	
	if passedParams.Dockerfile != "" {
		fmt.Println("Reader WAS BUILT WITH THE the dockerfile **!*@!@")
		return TarzipBufferFromDockerfile(passedParams.Dockerfile)
	} else {
		fmt.Println("REQUEST WAS BUILT WITH the url!!! **!*@!@")
		return ResponseZipFromURL(passedParams.ZipUrl)
	}

}


//URL example = https://github.com/tutumcloud/docker-hello-world/archive/v1.0.tar.gz
func ResponseZipFromURL(url string) io.ReadCloser {

	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	response, err := client.Do(req)
	if err != nil {
			log.Fatalln(err)
		}

	return response.Body

}


func TarzipBufferFromDockerfile(dockerfile string) *bytes.Buffer {

	// Create a buffer to write our archive to.
	buf := new(bytes.Buffer)

	// Create a new tar archive.
	tw := tar.NewWriter(buf)

	// Add the dockerfile to the archive.
	var files = []struct {
		Name, Body string
	}{
		{"Dockerfile", dockerfile},
	}
	for _, file := range files {
		hdr := &tar.Header{
			Name: file.Name,
			Size: int64(len(file.Body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			log.Fatalln(err)
		}
		if _, err := tw.Write([]byte(file.Body)); err != nil {
			log.Fatalln(err)
		}
	}
	//Check the error on Close.
	if err := tw.Close(); err != nil {
		log.Fatalln(err)
	}
	return buf
}