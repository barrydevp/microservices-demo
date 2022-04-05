const got = require('got')

const _delay = (ms) => new Promise((rs) => {
  setTimeout(rs, ms)
})

const constants = {
  ErrNotLeader: new Error('not leader'),
  SchemeHttp: "http",
  SchemeHttps: "https",
  DefaultPort: "8000",
  SysPrefixApiPath: "api/sys",
  MaxRetry: 10,
}

class Node {
  constructor(baseUrl) {
    this.baseUrl = baseUrl
    this.rest = got.extend({
      prefixUrl: baseUrl,
      headers: {
        'Content-Type': 'application/json',
      },
      resolveBodyOnly: true,
      responseType: 'json',
      // json: {},
    })
  }

  async init() {
    const nconf = await this.nconf()
    this.conf = nconf
  }

  async nconf() {
    const nconf = await this._request(
      (rest) => rest.get(constants.SysPrefixApiPath + "/nconf")
    )

    return nconf
  }

  async rsconf() {
    const rsconf = await this._request(
      (rest) => rest.get(constants.SysPrefixApiPath + "/rsconf")
    )

    return rsconf
  }

  async ping() {
    await this._request(
      (rest) => rest.get(constants.SysPrefixApiPath + "/ping")
    )

    return true
  }

  async _request(fn) {
    try {
      const resp = await fn(this.rest)
      return resp.data
    } catch (err) {
      const resp = err.response
      if (resp) {
        if (resp.body.err && resp.body.err.includes('node is not the leader')) {
          throw constants.ErrNotLeader
        }
        throw new Error(`non 200 status: ${resp.statusCode}, msg: ${resp.body.msg}, err: ${resp.body.err}`)
      }
      throw err
    }
  }
}

class ConnString {
  constructor(uri) {
    if (!uri) {
      uri = "http://localhost" + constants.DefaultPort
    }
    this.uri = uri
    const u = new URL(uri)
    this.url = u
    const scheme = u.protocol.replace(':', '')
    if (scheme !== constants.SchemeHttp && scheme !== constants.SchemeHttps) {
      throw new Error("scheme must be \"http\" or \"https\"")
    }

    this.scheme = scheme
    const rawHosts = u.host.split(",")
    if (!rawHosts.length) {
      throw new Error("empty host")
    }
    this.hosts = rawHosts.map(h => {
      return h + (h.includes(':') ? "" : `:${constants.DefaultPort}`)
    })
  }

  getBaseURL(host) {
    return this.scheme + "://" + host
  }
}

class Connection {
  constructor(uri) {
    this.connStr = new ConnString(uri)
  }

  async connect() {
    await this.loadNodes()
    await this.loadCluster()
  }

  async loadNodes() {
    const cs = this.connStr
    const nodes = await Promise.all(cs.hosts.map(async (host) => {
      const baseURL = cs.getBaseURL(host)
      const n = new Node(baseURL)
      await n.init()
      return n
    }))

    this.nodes = nodes

    return true
  }

  async loadCluster() {
    if (!Array.isArray(this.nodes) || this.nodes.lenth) {
      throw new Error('empty nodes')
    }

    // fetch rsconf
    const firstNode = this.nodes[0]
    const rsconf = await firstNode.rsconf()
    this.rsconf = rsconf

    // populate leader
    const leaderConf = rsconf.Leader
    if (!leaderConf) {
      throw new Error('no leader')
    }

    const leader = this.nodes.find((n) => {
      return n.conf.Host === leaderConf.Host && n.conf.ID === leaderConf.ID
    })

    if (!leader) {
      throw new Error('cannot found leader in uri')
    }

    this.leader = leader

    return true
  }

  async request(fn) {
    let resp, err
    const exec = async () => {
      resp = await this.leader._request(fn).catch((error) => {
        err = error
        return null
      })
    }

    await exec()
    let retries = 0
    while (retries < constants.MaxRetry && err === constants.ErrNotLeader) {
      // handle change leader by reload cluster
      await this.loadCluster()
      err = null
      resp = null
      await exec()
      retries++
      const delay = 200 ** retries
      await _delay(delay)
    }

    if (err) {
      throw err
    }

    return resp
  }
}

exports.Connection = Connection
