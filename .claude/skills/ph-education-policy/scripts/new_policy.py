#!/usr/bin/env python3
"""Generate a verification-safe policy-record template on stdout."""
import argparse
import json
ap=argparse.ArgumentParser(description='Generate a normalized policy-record template')
ap.add_argument('--id',required=True); ap.add_argument('--agency',required=True); ap.add_argument('--title',required=True)
a=ap.parse_args()
row={
 'id':a.id,'agency':a.agency.upper(),'issuance_type':'','number':None,'series_year':None,'title':a.title,
 'issued_at':None,'effective_from':None,'effective_until':None,'status':'UNKNOWN_REQUIRES_VERIFICATION',
 'topics':[],'scope':{},'relations':[],
 'source':{'official':False,'url':'','verified_at':None},
 'notes':'Verify against official full text/enclosures before changing production rules.'
}
print(json.dumps(row,ensure_ascii=False,indent=2))
