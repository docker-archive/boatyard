A Go web service for building, pushing, and purging docker images.

# **Running**

With docker and fig installed, simply run fig up tutum-builder.

# **Usage**

* The POST request builds an image in a docker instance, pushes it to a repository, then purges the image from the docker instance.  
* The Request requires an image_name and either a docker file or a tarUrl.  
* Username, password, and email are not necessarily required (private repos could be configured this way).  
* In order to push to a private repo the image_name should be of the form private.repo/namespace/image.  
* The tar file must have a dockerfile in the top level contents.

**Json Requests:**

	POST /api/v1/build
	{
	"image_name": "namespace/image",
	"username": "user",
	"password": "password",
	"email": "asdasd@gmail.com",
	"dockerfile": "FROM ubuntu:saucy\nCMD echo \"Hello world\""
	}
	
	POST /api/v1/build
	{
	"image_name": "namespace/image",
	"username": "user",
	"password": "password",
	"email": "asdasd@gmail.com",
	"tarUrl": "tarmusthaveadockerfile.com/files/hello-	world.tar.gz"
	}
	
**Multipart Request:**

	POST /api/v1/build HTTP/1.1
	Host: 127.0.0.1:8080
	Cache-Control: no-cache
	
	----WebKitFormBoundaryE19zNvXGzXaLvS5C
	Content-Disposition: form-data; name="TarFile"; filename="dockertarexample.tar.gz"
	Content-Type: application/x-gzip
	
	
	----WebKitFormBoundaryE19zNvXGzXaLvS5C
	Content-Disposition: form-data; name="Json"; filename="postmantest.json"
	Content-Type: application/json
	
	
	----WebKitFormBoundaryE19zNvXGzXaLvS5C	
		
	Where postmantest.json has the following format. 
	{
	"image_name": "namespace/image",
	"username": "user",
	"password": "password",
	"email": "asdasd@gmail.com"
	}
	

**Response:**

	{
	"JobIdentifier": "ef0c7a10-31a5-4140-6087-df97c2bebcb2"
	}

The JobIdentifier can be used to check the status and logs in a redis cache with the following GET requests.

**Request:**

	GET /api/v1/:jobid/status

**Response:**

	{
	  "Status": "Status is returned as a string."
	}

The status will catch errors, and on a successful call the job status has three stages, "Building", "Pushing", and "Finished".


**Request:**
	
	GET /api/v1/:jobid/logs

**Response:**
	
	{
 	 "Logs": "Logs are returned as one string."
	}
	
The Logs catches the build logs and any errors returned from the docker node. 


# **Environment Variables**

* The builder will use the local DOCKER_HOST with a default to http://127.0.0.1:4243 
* The builder will connect to the redis cache at CACHE_1_PORT_6379_TCP_ADDR + ":" + CACHE_1_PORT_6379_TCP_PORT with a default to :6379
* The builder will listen and serve at PORT with a default to :8080