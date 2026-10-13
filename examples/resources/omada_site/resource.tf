# Manages the name of the provider's site. It does not create or delete a
# site: removing this resource only stops Terraform management.
#
# Configure the provider's `site` by ID when renaming it, since a provider
# configured with the previous name can no longer resolve the site.
resource "omada_site" "this" {
  name = "example-site"
}
