package main


import (
    "github.com/ant0ine/go-json-rest/rest"
    "net/http"
 	"fmt"
 	"archive/tar"
	"bytes"
	"log"
	"os"
 	"io/ioutil"
)

type PassedParams struct {
    Image_name string
    Username string
    Password string
    Email string
    Dockerfile string
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

	//Unpack the params that come in.  Catch the error if it isn't nil.
	passedParams :=PassedParams{}
	err := r.DecodeJsonPayload(&passedParams)
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

 	urlString := (docker_node +"/build")

 	//Create the post request to build.
	client := &http.Client{}
	req, err := http.NewRequest("POST", urlString, buf)
	response, err := client.Do(req)
	contents, err := ioutil.ReadAll(response.Body)

	fmt.Printf("%s", contents)

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
