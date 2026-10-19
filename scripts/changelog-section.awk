# Prints one version's section of CHANGELOG.md, without its "## [x.y.z]"
# heading: the release notes for that version. release.yml uses it.
#
#   awk -v v=0.1.0 -f scripts/changelog-section.awk CHANGELOG.md
#
# A section starts at "## [<version>]" and ends at the next "## [" heading.
# Prints nothing when there is no such section, so callers can check for
# empty output. Blank lines at the start and end are left out.

# Every "## [" heading: start printing if it is our version, else stop.
# index(...) == 1 means the line starts with exactly "## [<version>]".
/^## \[/ {
	in_section = (index($0, "## [" v "]") == 1)
	next
}

# The link definitions at the bottom ("[0.1.0]: https://...") are not part
# of any section.
/^\[[^]]+\]: / {
	in_section = 0
}

in_section {
	# Hold blank lines back until a non-blank line follows, so the output
	# neither starts nor ends with blank lines.
	if ($0 == "") {
		if (printed) {
			blanks++
		}
		next
	}
	for (; blanks > 0; blanks--) {
		print ""
	}
	print
	printed = 1
}
