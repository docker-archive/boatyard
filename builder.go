package main


import (
    "github.com/ant0ine/go-json-rest/rest"
    "github.com/garyburd/redigo/redis"
    "github.com/nu7hatch/gouuid"
    "net/http"
 	"fmt"
 	"archive/tar"
	"bytes"
	"log"
	"os"
 	"io/ioutil"
 	"encoding/base64"
 	"encoding/json"

)

type PassedParams struct {
    Image_name string
    Username string
    Password string
    Email string
    Dockerfile string
}

type PushAuth struct {
    Username string `json:"username"`
    Password string `json:"password"`
    Serveraddress string `json:"serveraddress"`
    Email string `json:"email"`
}

type JobID struct {
    JobIdentifier string
}

func main() {

    handler := rest.ResourceHandler{
                EnableRelaxedContentType: true,
        }

    handler.SetRoutes(
        &rest.Route{"POST", "/api/v1/build", BuildImageFromDockerfile},

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

//Builds the image in a docker node.   
func BuildImageFromDockerfile(w rest.ResponseWriter, r *rest.Request) {

	//create a jobID named u4.
	u4, uuidErr := uuid.NewV4()
	if uuidErr != nil {
	    fmt.Println("error:", uuidErr)
	    return
	}
	// fmt.Println(u4)

	// s := u4.String()
	var jobid JobID
	jobid.JobIdentifier = u4.String()


	//Open a redis connection.  USE ENVIRONMENT VARIABLE SOON>< 


	// CACHE_1_PORT_6379_TCP_ADDR + ":" CACHE_1_PORT_6379_TCP_PORT are the variables.
	c, err := redis.Dial("tcp", ":6379")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()


	//Set the status to building.
	c.Do("HSET", jobid.JobIdentifier, "status", "Building")

	
	//Checks to see if we can actually get the status from the cache.
	// s, err := redis.String(c.Do("HGET", jobid.JobIdentifier, "status"))
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// fmt.Println(s + "WOOOOO!!!!")

	//Write the jobid back now that the cache is filled.
	w.WriteJson(jobid)


//Now let's start the build proccess.

	//Unpack the params that come in.
	passedParams :=PassedParams{}
	err = r.DecodeJsonPayload(&passedParams)
	if err != nil	{
		rest.Error(w, err.Error(), http.StatusInternalServerError)
	    return
	}
	
	//Returns the Tar buffer for the passed dockerfile.  Only needs to be done if 
	buf := TarzipBufferForDockerfile(passedParams.Dockerfile)


	//Essentially docker_node := os.Getenv("Docker_NODE") | default_node
    var docker_node string
    if os.Getenv("DOCKER_NODE") != "" {
    	docker_node = os.Getenv("DOCKER_NODE")
    } else {
    	docker_node = "http://127.0.0.1:4243"
    }

 	//Do I need to say -t to tag it? Yes.
 	buildUrl := (docker_node +  "/v1.10/build?t=" + passedParams.Image_name)

 	//Create the post request to build.
	buildClient := &http.Client{}
	buildReq, err := http.NewRequest("POST", buildUrl, buf)
	buildResponse, err := buildClient.Do(buildReq)
	buildContents, err := ioutil.ReadAll(buildResponse.Body)

	fmt.Printf("%s", buildContents)









//Build complete.  Let's change the status to pushing, and then start the push process.

	c.Do("HSET", jobid.JobIdentifier, "status", "Pushing")

	//Encoder stuff.  Pass this as the header.
	// data := (passedParams.Username + ":" + passedParams.Password)
	var data PushAuth
	data.Username = passedParams.Username
	data.Password = passedParams.Password
	data.Email = passedParams.Email
	data.Serveraddress = "https://index.docker.io/v1/"

	jsonData, err := json.Marshal(data)
	if err != nil {
		fmt.Println("error:", err)
	}
	fmt.Printf("%s", jsonData)


	// fmt.Println(data)
	sEnc := base64.StdEncoding.EncodeToString([]byte(jsonData))
    fmt.Println(sEnc)

	pushUrl := (docker_node + "/v1.10/images/" + passedParams.Image_name + "/push")
	fmt.Println(pushUrl)
	// pushUrl := (docker_node + "/v1.10/images/ubuntu/push")


	pushClient := &http.Client{}
	pushReq, err := http.NewRequest("POST", pushUrl, nil)

	pushReq.Header.Add("X-Registry-Auth", sEnc)

	pushResponse, err := pushClient.Do(pushReq)
	pushContents, err := ioutil.ReadAll(pushResponse.Body)
	if err != nil	{
		log.Fatal(err)
		}
	
	fmt.Printf("%s", pushContents)

	c.Do("HSET", jobid.JobIdentifier, "status", "Finished")




}


func TarzipBufferForDockerfile(dockerfile string)  *bytes.Buffer {

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
