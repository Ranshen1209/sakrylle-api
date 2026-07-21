import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import batchImage from './batchImage'
import sakrylle from './sakrylle'
import admin from './admin'
import misc from './misc'

export default {
  ...landing,
  ...common,
  ...dashboard,
  ...batchImage,
  ...sakrylle,
  admin,
  ...misc,
}
