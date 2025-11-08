#!/bin/sh

latest_tag=$(git describe --tags --abbrev=0)
echo "Latest tag is ${latest_tag}"
read -p "Enter new release tag (naming strategy: https://shorturl.at/ciuN2): " new_tag
git tag ${new_tag}
git push origin ${new_tag}
echo "Released new tag ${new_tag}"