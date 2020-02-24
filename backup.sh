#!/bin/bash
usage () {
  echo "$0 cluster [environment]"
  echo "  Required cluster is either 'edit' or 'public'"
  echo "  Optional environment is either 'staging' or 'production'"
  echo "  Default environment is production."
}

alert() { # parameters: subject, content
  local _subject="$1"
  local _content="$2"
  echo "$_content" | mail -s "$_subject" "$email_addrs" 2>/dev/null
}

alertfile() { # parameters: subject, file
  local _subject="$1"
  local _content="$2"
  local _file="$3"
  local _length="$(wc -l $_file | cut -d' ' -f1)"
  ( echo "$_content"
    if [ "$_length" -gt 10 ]; then
      head "$_file"
      echo '...'
    else
      cat "$_file"
    fi ) | mail -s "$_subject" "$email_addrs" 2>/dev/null
}

# set environment variables.
. ~/.env

echo "`date +'%F %T'` BACKUP PROCESS START-TIME"

####################################
#    Setup URLs used for backup    #
####################################
unset urls
cluster="$1"
env="${2:-production}"
split=""
if [ "$env" = 'staging' ]; then
  if [ "$cluster" = 'edit' ]; then
    # Gato Edit base URLs
    urls=("${urls_staging_edit[@]}")
    split="false"
    gsize="2"
  elif [ "$cluster" = 'public' ]; then
    # Gato Public base URLs
    urls=("${urls_staging_public[@]}")
    #split="true"
    split="false"
    gsize="2"
  else
    echo "Unknown cluster"
    usage
    exit 1
  fi
elif [ "$env" == 'production' ]; then
  if [ "$cluster" = 'edit' ]; then
    # Gato Edit base URLs
    urls=("${urls_production_edit[@]}")
    split="false"
    gsize="1"
  elif [ "$cluster" = 'public' ]; then
    # Gato Public base URLs
    urls=("${urls_production_public[@]}")
    #split="true"
    split="false"
    gsize="2"
  else
    echo "Unknown cluster"
    usage
    exit 1
  fi
else
   echo "Unknown environment"
   usage
   exit 1
fi
wdir=$HOME/$env/$cluster

# Get a session from each host.
sessionidx=0
sessionmax=0
for url in "${urls[@]}"; do
  # Using tail as old production edit box redirects us to login; thus we get multiple session keys.
  #   We want to use the last one which is the logged in session.
  #session="`curl -i -L --fail --user $user:$pswd --silent $url/.magnolia/pages/adminCentral.html | sed -n 's/^Set-Cookie: JSESSIONID=\([^ ;]\+\).*$/JSESSIONID=\1/p' | tail -1`"
  session="`curl -i -L --fail --user $user:$pswd --silent $url | sed -n 's/^Set-Cookie: JSESSIONID=\([^ ;]\+\).*$/JSESSIONID=\1/p' | tail -1`"
  if [ "$session" != "" ]; then
    echo "`date +'%F %T'` INFO: Added session = $url,$session" >&2
    sessions[${#sessions[@]}]="$url,$session"
  else
    echo "`date +'%F %T'` WARNING: Failed session for $url URL." >&2
  fi
done

# Must have at least one session to start backups
sessionmax=${#sessions[@]}
if [ $sessionmax -lt 1 ]; then
  content="$(date) ERROR: Failed to get any sessions for $cluster backup. No sites were backed up."
  echo "$content" >&2
  alert "Gato $cluster backup failure." "$content"
  exit 1
else
  sessionmax=$(( sessionmax - 1 ))
fi

###############################
#  Start Gato backup process  #
###############################
# To make better use of queues we try to keep all
# pipes full. The best way is to start with largest
# workspace files and finish with the smallest ones;
# thus feed node list to backup process in following order:
# websites, userroles, users, usergroups, gatoapps, resources
timestamp=$(date -I)
# Start fresh backup of repo, i.e. Remove all previous markers of completed backups
rm -f "$wdir/"{list.failed,save.failed,files-removed}
mkdir -p "$wdir/log" "$wdir/removed"
for repo in website userroles users usergroups gatoapps resources; do
  mkdir -p "$wdir/registry/$repo" "$wdir/data/$repo"
  rm -f "$wdir/registry/$repo/"*.xml.bu
done
# TODO: figure out how to get workspace list to backup
# Remove dam because files are too large, let pagers application download those per node.
#for rdp in 'website:1:/,page' 'dam:1:/,folder' 'dms:1:/,content' 'config:2:/modules/gato,\(contentNode|content\)' 'config:2:/modules/dms,\(contentNode|content\)' 'userroles:1:/,role' 'users:1:/admin,user' 'usergroups:1:/,group'; do
for rdp in 'website:1:/,page' 'userroles:1:/,role' 'users:1:/admin,user' 'usergroups:1:/,group' 'gatoapps:1:/,content' 'resources:1:/,folder'; do
  # Generate list of nodes to backup
  repo="${rdp%%:*}"
  path="${rdp##*:}"
  search="${path##*,}"
  path="${path%%,*}"
  depth="${rdp#*:}"
  depth="${depth%:*}"
  # Round Robin between gato servers for list of nodes to backup
  sessionidx=$(( (sessionidx >= sessionmax) ? 0 : sessionidx + 1 ))
  url_session="${sessions[$sessionidx]}"
  url="${url_session%%,*}"
  session="${url_session##*,}"
  # Add non-page node type website content.
  if [ "$repo" == 'website' ]; then
    echo 'website.homepage-data'
    echo 'website.global-data'
  fi
  # Verify curl succeeded, otherwise backup is incomplete and potentially left in a bad state.
  ( curl --silent --fail --cookie "$session" "$url/docroot/gato/dump.jsp?depth=$depth&path=$path&repository=$repo"
    if [ $? -ne 0 ]; then touch "$wdir/list.failed"; fi ) |
  sed -ne 's_/_._g; s_^\.\([[:alnum:]._-]\+\)\[mgnl:'"$search"'\]$_'"$repo"'.\1_p'
done |
while read node; do
  # Create node marker file to help find removed nodes.
  ########### uncomment to test out bad nodes
  #if [ "$node" == "website.testing-site-destroyer" ]; then echo "website.bad-test"; fi
  ###########
  repo="${node%%.*}"
  name="${node#*.}"
  touch "$wdir/registry/$repo/$repo.$name.xml.bu"
  echo "$node"
done | ~/bin/gatobackup "--split=$split" "--groupsize=$gsize" "--stamp=$timestamp" "--workdir=$wdir" "${sessions[@]}"
if [ $? -ne 0 ]; then
  alert "Gato $cluster backup failure" "ERROR: Backup process was unable to start."
  exit 1
fi

# Check if retrieving list of nodes to backup had some failures.
if [ -f "$wdir/list.failed" ]; then
  alert "Gato $cluster backup failure" "ERROR: Backup process was unable to successfully retrieve full list of nodes. All or some of the nodes may not have been backed up."
  exit 1
fi

# Check if during saving nodes some failures occurred.
if [ -s "$wdir/save.failed" ]; then
  alertfile "Gato $cluster backup failure" "ERROR: unable to save some sites" "$wdir/save.failed"
  exit 1
fi

echo "`date +'%F %T'` INFO: Start locating removed nodes."
####################################################
#  Find nodes that may have been removed from Gato  #
#####################################################
# Create a list of nodes that exist without their
# associated backup marker. These nodes are no
# longer being backed up as they have probably
# been removed from magnolia.
# NOTE: A backup or node list error would
#   mean that not all nodes had a chance to
#   backup and thus the backup markers would
#   not be created for those nodes. We thus
#   cannot determine if nodes with no marker
#   have been removed.
# Only checking website workspaces/repos.
# Find all node hash (sha1) files without an associated backup (bu) marker file.
find "$wdir/" -type f -regex '.*/\(website\)/.*\.xml\.\(sha1\|bu\)' |
  sort |
  awk 'BEGIN {RS = ".xml.sha1\n"; FS = ".xml.bu\n"} {if (NF == 1) print $1} { if (NF > 1 && $(NF -1) != $NF && $NF != "") print $NF}' > "$wdir/files-removed"
if [ -s "$wdir/files-removed" ]; then
  echo "`date +'%F %T'` INFO: Adding removed files log to full log"
  cat "$wdir/files-removed"
  alertfile "Gato $cluster sites removed" "WARNING: Some nodes where not found on backup list and may have been removed from Gato $cluster" "$wdir/files-removed"
  # Move hash files for nodes that are no longer being backed up.
  while read file; do
    mv "${file}.xml.sha1" "$wdir/removed/"
  done < "$wdir/files-removed"
else
  echo "`date +'%F %T'` INFO: No nodes detected as having been removed from Gato $cluster."
fi
echo "`date +'%F %T'` BACKUP PROCESS END-TIME"
