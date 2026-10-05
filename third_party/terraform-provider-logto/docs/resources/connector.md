---
page_title: "logto_connector Resource"
subcategory: ""
description: |-
  A Logto connector; configuration is sensitive and becomes a Kubernetes Secret reference in Upjet.
---

# logto_connector

A Logto connector; configuration is sensitive and becomes a Kubernetes Secret reference in Upjet.

`configuration` is a JSON object containing the API fields this resource owns.
Omitted fields remain unmanaged. Import uses the connector API ID.
Connector factory `connector_id` changes require replacement.
