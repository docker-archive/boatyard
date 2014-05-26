A Go web service which provides a HTTP API and a web UI for building, pushing, and deleting docker images.

# Running

With docker and fig installed, and with docker listening in `tcp://localhost:4243`, simply run:

	fig up -d

Run `docker ps` and check the allocated port for boatyard. Then point your browser to `http://localhost:49XXX/` to open the web UI.

# Configuration options

You can specify any of the following environment variables in the fig.yml to configure it:

* `DOCKER_HOST` (default: `tcp://localhost:4243`): the endpoint where a Docker server is listening to
* `CACHE_1_PORT_6379_TCP_ADDR` (default: none): the hostname to connect to a Redis cache
* `CACHE_1_PORT_6379_TCP_PORT` (default: `6379`): the port to connect to a Redis cache
* `CACHE_PASSWORD` (default: none): an optional password to authenticate with the Redis cache

# Usage

The primary inputs to create an image are:

* Image name (i.e. `user/myimage`)
* Username (to be used when pushing, i.e. `user`)
* Password (to be used when pushing, i.e. `password`)
* Email (to be used when pushing, i.e. `user@example.com`)

Any of the following inputs can be passed to **boatyard** to create the image:

* A `Dockerfile`
* A URL to a tarball containing a `Dockerfile` on the root folder and any required files
* A GitHub repository (combination of GitHub username, repo name and tag)
* A tarball sent as part of the request

# API specification

## Building

### Requests

From `Dockerfile`:

	POST /api/v1/build

	{
		"image_name": "user/image",
		"username": "user",
		"password": "password",
		"email": "user@example.com",
		"dockerfile": "FROM ubuntu:saucy\nCMD echo \"Hello world\""
	}

From a tarball URL:

	POST /api/v1/build

	{
		"image_name": "user/image",
		"username": "user",
		"password": "password",
		"email": "user@example.com",
		"tar_url": "https://github.com/tutumcloud/docker-hello-world/archive/v1.0.tar.gz"
	}

From GitHub repository:

	POST /api/v1/build

	{
		"image_name": "user/image",
		"username": "user",
		"password": "password",
		"email": "user@example.com",
		"github_username": "tutumcloud",
		"github_reponame": "docker-hello-world",
		"github_tag": "v1.0"
	}

From a tarball sent with the request:

	POST /api/v1/build
	
	----WebKitFormBoundaryE19zNvXGzXaLvS5C
	Content-Disposition: form-data; name="TarFile"; filename="dockertarexample.tar.gz"
	Content-Type: application/x-gzip
	
	
	----WebKitFormBoundaryE19zNvXGzXaLvS5C
	Content-Disposition: form-data; name="Json"; filename="manifest.json"
	Content-Type: application/json
	
	
	----WebKitFormBoundaryE19zNvXGzXaLvS5C	
		
Where manifest.json has the following format. 

	{
		"image_name": "user/image",
		"username": "user",
		"password": "password",
		"email": "user@example.com"
	}
	

### Response

	{
		"JobIdentifier": "ef0c7a10-31a5-4140-6087-df97c2bebcb2"
	}

The `JobIdentifier` can be used to check the status and logs with the following GET requests.

## Checking job status

### Request

Issue the following request:

	GET /api/v1/:jobid/status

where `:jobid` is the job ID returned in the build request (i.e. `ef0c7a10-31a5-4140-6087-df97c2bebcb2`)

### Response

	{
		"Status": "Pushing"
	}

The status will show errors, and on a successful call the job status has three stages: "Building", "Pushing", and "Finished".

## Checking job logs

### Request

Issue the following request:

	GET /api/v1/:jobid/logs

where `:jobid` is the job ID returned in the build request (i.e. `ef0c7a10-31a5-4140-6087-df97c2bebcb2`)

### Response
	
	{
 		"Logs": "Logs are returned as one string."
	}
	
The logs catch the build logs and any errors returned from the docker host. 
