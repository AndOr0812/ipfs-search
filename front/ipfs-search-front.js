const elasticsearch = require('elasticsearch');
const http = require('http');
const url = require('url');
const htmlEncode = require('js-htmlencode');

const port = 9615;

var client = new elasticsearch.Client({
  host: 'localhost:9201',
  log: 'trace'
});

function query(q, page) {
  var body = {
      "query": {
          "query_string": {
              "query": q,
              "default_operator": "AND"
          }
      },
      "highlight": {
          "order" : "score",
          "require_field_match": false,
          "encoder": "html",
          "fields": {
              "*": {
                  "number_of_fragments" : 1
              }
          }
      },
      "_source": [
        "metadata.title", "metadata.name", "metadata.description",
        "references"
      ]
  }

  const size = 15;

  return client.search({
    index: 'ipfs',
    body: body,
    size: size,
    from: page*size
  });
}

function error_response(response, code, error) {
  console.trace(code+": "+error.message);

  response.writeHead(code, {"Content-Type": "application/json"});
  response.write(JSON.stringify({
    "error": error
  }));
  response.end();
}

function get_title(result) {
  // Get title from result

  // Highlights take preference
  var hl = result.highlight;

  if (hl) {
    const highlight_priority = [
      "metadata.title", "references.name"
    ]

    // Return the first one from the priority list
    for (var i=0; i<highlight_priority.length; i++) {
      if (hl[highlight_priority[i]]) {
        return hl[highlight_priority[i]][0];
      }
    }
  }

  // Try metadata
  var src = result._source;
  var titles = [];

  if ("metadata" in src) {
    const metadata_priority = [
      "title", "name"
    ]

    metadata_priority.forEach(function (item) {
      if (src.metadata[item]) {
        titles.push(src.metadata[item]);
      }
    });
  }

  // Try references
  src.references.forEach(function (item) {
    if (item.name) {
      titles.push(item.name);
    }
  });

  // Pick longest title
  if (titles.length > 0) {
    titles.sort(function (a, b) { return b.length - a.length });

    return htmlEncode.htmlEncode(titles[0]);
  } else {
    // Fallback to id
    return htmlEncode.htmlEncode(result._id);
  }
}

function get_description(result) {
  // Use highlights, if available
  if (result.highlight) {
    if (result.highlight.content) {
      return result.highlight.content;
    }

    if (result.highlight["links.Name"]) {
      // Reference name matching
      return "Links to &ldquo;"+result.highlight["links.Name"]+"&rdquo;";
    }

    if (result.highlight["links.Hash"]) {
      // Reference name matching
      return "Links to &ldquo;"+result.highlight["links.Hash"]+"&rdquo;";
    }
  }

  var metadata = result._source.metadata;
  if (metadata) {
    // Description, if available
    if (metadata.description) {
      return htmlEncode.htmlEncode(metadata.description);
    }

  }

  // Default to nothing
  return null;
}

function transform_results(results) {
  var hits = [];

  results.hits.forEach(function (item) {
    hits.push({
      "hash": item._id,
      "title": get_title(item),
      "description": get_description(item)
    })
  });

  // Overwrite existing list of hits
  results.hits = hits;
}

console.info("Starting server on http://localhost:"+port+"/");

http.createServer(function(request, response) {
  var parsed_url;

  try {
    console.log(request.url);

    try {
      parsed_url = url.parse(request.url, true);
    } catch(err) {
      error_response(response, 400, err.message);
    }

    if (parsed_url.pathname === "/search") {
      if (!"q" in parsed_url.query) {
        error_response(response, 422, "query argument missing");
      }

      var page = 0;
      const max_page = 100;

      if ("page" in parsed_url.query) {
        var page = parseInt(parsed_url.query.page, 10);

        // For performance reasons, don't allow paging too far down
        if (page > 100) {
          error_response(422, "paging not allowed beyond 100");
        }
      }

      query(parsed_url.query.q, page).then(function (body) {
        console.info("200: Returning "+body.hits.hits.length+" results");

        transform_results(body.hits);

        response.writeHead(200, {"Content-Type": "application/json"});
        response.write(JSON.stringify(body.hits));
        response.end();
      }, function (error) {
        throw(error);
      });

    } else {
        error_response(response, 404, "file not found");
    }

  } catch(err) {
    // Catch generic errors
    error_response(response, 500, err.message);
  } finally {

  }
}).listen(port);

