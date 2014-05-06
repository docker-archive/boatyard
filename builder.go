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
	"bufio"
	"log"
	"net/http"
	"os"
	"strings"
	"net/http/httputil"
	"net"
	"io"
	"io/ioutil"
)

type PassedParams struct {
	Image_name string
	Username   string
	Password   string
	Email      string
	Dockerfile string
	TarUrl 	   string
	TarFile    []byte
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
	ErrorDetail ErrorCatcher `json:"errorDetail"`
	Stream      string `json:"stream"`
}

type ErrorCatcher struct {
	Message 	string `json:"message"`
	Error 		string `json:"error"`
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

	//First check if the key and field exist.
	exists, err := redis.Bool(c.Do("HEXISTS", jobid, "status")) 
	if exists == true {
		var status JobStatus
		status.Status, err = redis.String(c.Do("HGET", jobid, "status"))
		if err != nil {
			rest.Error(w, "Redis Cache Error", 404)
		}
		w.WriteJson(status)
	} else {
		rest.Error(w, "Jobid doesn't exist in the cache.  Bad request.", 404)
	}
}



//3 steps.  Unpack the jobId that came in.  Open the redis connection and get the logs.  Then write the logs back.
func GetLogsForJobID(w rest.ResponseWriter, r *rest.Request) {
	jobid := r.PathParam("jobid")

	//Open a redis connection.  c is type redis.Conn
	c := RedisConnection()
	defer c.Close()

	//First check if the key and field exist.
	exists, err := redis.Bool(c.Do("HEXISTS", jobid, "logs"))
	if exists == true {
		var logs JobLogs
		logs.Logs, err = redis.String(c.Do("HGET", jobid, "logs"))
		if err != nil {
			rest.Error(w, "Redis Cache Error", 404)
		}
		w.WriteJson(logs)
	} else {
		rest.Error(w, "Jobid doesn't exist in the cache.  Bad request.", 404)
	}
}

//Builds the image in a docker node.
func BuildImageFromDockerfile(w rest.ResponseWriter, r *rest.Request) {

	passedParams := PassedParams{}

	//If the Content-Type is multipart, parse it.  Otherwise unpack the json.
	if strings.Contains(r.Header.Get("Content-Type"), "multipart") == true { 

		form, err := r.MultipartReader()
		TarPart, err := form.NextPart()
		TarData, err := ioutil.ReadAll(TarPart)
		JsonPart, err := form.NextPart()
		JsonData, _ := ioutil.ReadAll(JsonPart)

		err = json.Unmarshal(JsonData, &passedParams)
		if err != nil {
			rest.Error(w, err.Error(), 400)
		}
		passedParams.TarFile = TarData

	} else {
		//Unpack the params that come in.
		err := r.DecodeJsonPayload(&passedParams)
		if err != nil {
			rest.Error(w, err.Error(), 400)
			return
		}
	}

	if Validate(passedParams) {
		//Create a uuid for the specific job.
		var jobid JobID
		jobid.JobIdentifier = JobUUIDString()

		//Open a redis connection.  c is type redis.Conn
		c := RedisConnection()

		//Set the status to building in the cache.
		c.Do("HSET", jobid.JobIdentifier, "status", "Building")

		//Launch a goroutine to build, push, and delete.  Updates the cache as the process goes on.
		//Also pass it the file that came from the CLI?
		go BuildPushAndDeleteImage(jobid.JobIdentifier, passedParams, c)

		//Write the jobid back
		w.WriteJson(jobid)
	} else {
		//Params didn't validate.  Bad Request.
		rest.Error(w, "Insufficient Information.  Must provide at least an Image Name and a Dockerfile/Tarurl.", 400)
	}


}

func BuildPushAndDeleteImage(jobid string, passedParams PassedParams, c redis.Conn) {

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
	dockerConnection := httputil.NewClientConn(dockerDial, nil)
	buildReq, err := http.NewRequest("POST", buildUrl, ReaderForInputType(passedParams))
    buildResponse, err := dockerConnection.Do(buildReq)

    defer buildResponse.Body.Close()
	buildReader := bufio.NewReader(buildResponse.Body)
	if err != nil {
		c.Do("HSET", jobid, "status", err)
		return
	}

	var logsString string
	//Loop through.  If stream is there append it to the logsString and update the cache.
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
		if stream.ErrorDetail.Message != "" {


			buildLogsSlice := []byte(logsString)
			buildLogsSlice = append(buildLogsSlice, []byte(stream.ErrorDetail.Message)...)
			logsString = string(buildLogsSlice)
			c.Do("HSET", jobid, "logs", logsString)

			CacheBuildError := "Failed: " + stream.ErrorDetail.Message
			c.Do("HSET", jobid, "status", CacheBuildError)
			return
		}

		if stream.Stream != "" {
			buildLogsSlice := []byte(logsString)
			buildLogsSlice = append(buildLogsSlice, []byte(stream.Stream)...)
			logsString = string(buildLogsSlice)
			c.Do("HSET", jobid, "logs", logsString)

		}
	}
	
	//Update status in the cache, then start the push process.
	c.Do("HSET", jobid, "status", "Pushing")

	pushUrl := ("/v1.10/images/" + passedParams.Image_name + "/push")
	// pushConnection := httputil.NewClientConn(dockerDial, nil)
	pushReq, err := http.NewRequest("POST", pushUrl, nil)
	pushReq.Header.Add("X-Registry-Auth", StringEncAuth(passedParams, ServerAddress(splitImageName[0])))
    pushResponse, err := dockerConnection.Do(pushReq)
	pushReader := bufio.NewReader(pushResponse.Body)
	if err != nil {
		c.Do("HSET", jobid, "status", err)
		return
	}

	//Loop through.  Only concerned with catching the error.  Append it to logsString if it exists.
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

		//This if catches the error from docker and puts it in logs and status in the cache, then fails.
		if stream.ErrorDetail.Message != "" {
			pushLogsSlice := []byte(logsString)
			pushLogsSlice = append(pushLogsSlice, []byte(stream.ErrorDetail.Message)...)
			logsString = string(pushLogsSlice)
			c.Do("HSET", jobid, "logs", logsString)
			CachePushError := "Failed: " + stream.ErrorDetail.Message
			c.Do("HSET", jobid, "status", CachePushError)
			return
		}
	}

	//Finished.  Update status in the cache and close.
	c.Do("HSET", jobid, "status", "Finished")
	c.Close()

	//Delete it from the docker node.  Save space.  
	deleteUrl := ("/v1.10/images/" + passedParams.Image_name)
	deleteReq, err := http.NewRequest("DELETE", deleteUrl, nil)
    dockerConnection.Do(deleteReq)
	dockerConnection.Close()
}

func Validate(passedParams PassedParams) bool {
//Must have an image name and either a Dockerfile or TarUrl.
	switch {
		case passedParams.Dockerfile == "" && passedParams.TarUrl == "" && passedParams.TarFile == nil: 
			return false
		case passedParams.Image_name == "": 
			return false
		default:
			return true
	}
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
	} else {
		docker_node = "http://127.0.0.1:4243"
	}
	return docker_node
}


func Dial() net.Conn {
	var docker_proto string
	var docker_host string
	if os.Getenv("DOCKER_HOST") != "" {
		dockerHost := os.Getenv("DOCKER_HOST")
		splitStrings := strings.SplitN(dockerHost, "://", 2)
		docker_proto = splitStrings[0]
		docker_host = splitStrings[1]
	} else {
		docker_proto = "tcp"
		docker_host = "localhost:4243"
	}

	dockerDial, err := net.Dial(docker_proto, docker_host)
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

	if os.Getenv("CACHE_PASSWORD") != "" {
		c.Do("AUTH", os.Getenv("CACHE_PASSWORD"))
	}

	return c
}

//create a Unique JobID and return it as a string.
func JobUUIDString() string {
	uniqueJobId, uuidErr := uuid.NewV4()
	if uuidErr != nil {
		log.Fatal(uuidErr)
	}
	s := uniqueJobId.String()
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
	return cacheTCPAddress + ":" + cachePort
}

func ServerAddress(privateRepo string) string {

	//The server address is different for a private repo.
	var serveraddress string
	if privateRepo != "" {
		serveraddress = ("https://" + privateRepo + "/v1/")
	} else {
		serveraddress = "https://index.docker.io/v1/"
	}
	return serveraddress

}

//Reader will read from either the zip made from the dockerfile passed in or the zip from the url passed in.
func ReaderForInputType(passedParams PassedParams) io.Reader {

	//Use a switch.  one case for Dockerfile, one for TarUrl, one for Tarfile from client???

	switch {
		case passedParams.Dockerfile != "": 
			return ReaderForDockerfile(passedParams.Dockerfile)
		case passedParams.TarFile  != nil: 
			return bytes.NewReader(passedParams.TarFile)
		case passedParams.TarUrl != "":
			return ReaderForTarUrl(passedParams.TarUrl)
		default:
			return nil
		}
}


//URL example = https://github.com/tutumcloud/docker-hello-world/archive/v1.0.tar.gz
func ReaderForTarUrl(url string) io.ReadCloser {

	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	response, err := client.Do(req)
	if err != nil {
			log.Fatalln(err)
		}
	return response.Body

}


func ReaderForDockerfile(dockerfile string) *bytes.Buffer {

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
